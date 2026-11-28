// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package daemon_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// recordingCore stubs the two recording methods (issue #1783).
type recordingCore struct {
	kukeonv1.FakeClient

	doc  v1beta1.ContainerDoc
	req  kukeonv1.RecordingSegmentRequest
	list kukeonv1.ContainerRecording
	seg  kukeonv1.RecordingSegmentResult
	err  error
}

func (c *recordingCore) ListContainerRecording(
	_ context.Context, doc v1beta1.ContainerDoc,
) (kukeonv1.ContainerRecording, error) {
	c.doc = doc
	return c.list, c.err
}

func (c *recordingCore) ReadContainerRecordingSegment(
	_ context.Context, doc v1beta1.ContainerDoc, req kukeonv1.RecordingSegmentRequest,
) (kukeonv1.RecordingSegmentResult, error) {
	c.doc, c.req = doc, req
	return c.seg, c.err
}

func TestRecordingRPCRoundTrip(t *testing.T) {
	stored := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0xff, '\n', 0xc3}
	core := &recordingCore{
		list: kukeonv1.ContainerRecording{
			Format: kukeonv1.RecordingFormatAsciicast,
			Segments: []kukeonv1.RecordingSegment{
				{Seq: 7, Size: 600000, Compressed: true},
				{Seq: 8, Size: 42, Live: true},
			},
		},
		seg: kukeonv1.RecordingSegmentResult{
			Data: stored, Start: 4, Next: 12, Size: 600000, More: true, Compressed: true,
		},
	}
	client := kukeonv1.NewUnixClient(startKukeond(t, shortTempDir(t), core))
	t.Cleanup(func() { _ = client.Close() })
	ctx := testCtx(t)
	doc := v1beta1.ContainerDoc{
		Metadata: v1beta1.ContainerMetadata{Name: "work"},
		Spec:     v1beta1.ContainerSpec{ID: "work", RealmID: "r", SpaceID: "s", StackID: "st", CellID: "c"},
	}

	listed, err := client.ListContainerRecording(ctx, doc)
	if err != nil || listed.Format != kukeonv1.RecordingFormatAsciicast || len(listed.Segments) != 2 ||
		listed.Segments[0] != core.list.Segments[0] || listed.Segments[1] != core.list.Segments[1] {
		t.Fatalf("ListContainerRecording = %+v, %v", listed, err)
	}
	if core.doc.Spec.CellID != "c" || core.doc.Metadata.Name != "work" {
		t.Fatalf("doc %+v", core.doc)
	}

	req := kukeonv1.RecordingSegmentRequest{Seq: 7, Offset: 4, MaxBytes: 8}
	seg, err := client.ReadContainerRecordingSegment(ctx, doc, req)
	if err != nil || core.req != req {
		t.Fatalf("ReadContainerRecordingSegment = %+v, %v (req %+v)", seg, err, core.req)
	}
	if !bytes.Equal(seg.Data, stored) || seg.Start != 4 || seg.Next != 12 || seg.Size != 600000 ||
		!seg.More || !seg.Compressed || seg.Live {
		t.Fatalf("segment %+v", seg)
	}

	for _, sentinel := range []error{
		errdefs.ErrRecordingUnavailable, errdefs.ErrRecordingSegmentGone, errdefs.ErrContainerNotFound,
	} {
		core.err = fmt.Errorf("%w: work", sentinel)
		_, listErr := client.ListContainerRecording(ctx, doc)
		_, readErr := client.ReadContainerRecordingSegment(ctx, doc, req)
		if !errors.Is(listErr, sentinel) || !errors.Is(readErr, sentinel) {
			t.Errorf("%v: list %v, read %v", sentinel, listErr, readErr)
		}
	}
}
