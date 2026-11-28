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

package local

import (
	"context"
	"fmt"

	"github.com/eminwux/kukeon/internal/containerlog"
	"github.com/eminwux/kukeon/internal/containerrecording"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// ListContainerRecording implements kukeonv1.Client (issue #1783). The
// recording is the capture resolveContainerStream picks — the same file
// `kuke log` reads — with its rotated segments, so its format is known from
// the file name.
func (c *Client) ListContainerRecording(
	_ context.Context, doc v1beta1.ContainerDoc,
) (kukeonv1.ContainerRecording, error) {
	stream, err := c.recordingStream(doc)
	if err != nil {
		return kukeonv1.ContainerRecording{}, err
	}
	segments, err := containerrecording.List(stream.path)
	if err != nil {
		return kukeonv1.ContainerRecording{}, fmt.Errorf("list recording %s: %w", stream.path, err)
	}
	return kukeonv1.ContainerRecording{Format: recordingFormat(stream.format), Segments: segments}, nil
}

// ReadContainerRecordingSegment implements kukeonv1.Client (issue #1783).
func (c *Client) ReadContainerRecordingSegment(
	_ context.Context, doc v1beta1.ContainerDoc, req kukeonv1.RecordingSegmentRequest,
) (kukeonv1.RecordingSegmentResult, error) {
	stream, err := c.recordingStream(doc)
	if err != nil {
		return kukeonv1.RecordingSegmentResult{}, err
	}
	return containerrecording.ReadSegment(stream.path, req)
}

// recordingStream resolves the capture of an Attachable container; any other
// container has no recording.
func (c *Client) recordingStream(doc v1beta1.ContainerDoc) (containerStream, error) {
	stream, err := c.resolveContainerStream(doc)
	if err != nil {
		return containerStream{}, err
	}
	if !stream.attachable {
		return containerStream{}, fmt.Errorf(
			"%w: container %q is not attachable", errdefs.ErrRecordingUnavailable, doc.Metadata.Name,
		)
	}
	return stream, nil
}

func recordingFormat(format string) string {
	if format == containerlog.FormatAsciicast {
		return kukeonv1.RecordingFormatAsciicast
	}
	return kukeonv1.RecordingFormatRaw
}
