package secret_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/create/secret"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type fakeClient struct {
	kukeonv1.FakeClient
	createSecretFn func(doc v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error)
}

func (f *fakeClient) CreateSecret(_ context.Context, doc v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error) {
	if f.createSecretFn == nil {
		return kukeonv1.CreateSecretResult{}, errors.New("unexpected CreateSecret call")
	}
	return f.createSecretFn(doc)
}

func TestNewSecretCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name     string
		args     []string
		setup    func(t *testing.T, cmd *cobra.Command)
		clientFn func(doc v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error)
		wantErr  string
		wantOut  string
	}{
		{
			name: "from literal happy path",
			args: []string{"mysecret", "--from-literal=KEY=myvalue"},
			clientFn: func(doc v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error) {
				if doc.Metadata.Name != "mysecret" {
					t.Errorf("unexpected name: %q", doc.Metadata.Name)
				}
				if doc.Spec.Data != "myvalue" {
					t.Errorf("unexpected data: %q", doc.Spec.Data)
				}
				return kukeonv1.CreateSecretResult{
					Secret:  doc,
					Created: true,
				}, nil
			},
			wantOut: `Secret "mysecret" (realm "default", space "", stack "")`,
		},
		{
			name: "from literal without key (bare value)",
			args: []string{"bareval", "--from-literal=justvalue"},
			clientFn: func(doc v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error) {
				if doc.Spec.Data != "justvalue" {
					t.Errorf("unexpected data: %q", doc.Spec.Data)
				}
				return kukeonv1.CreateSecretResult{
					Secret:  doc,
					Created: true,
				}, nil
			},
			wantOut: `Secret "bareval" (realm "default", space "", stack "")`,
		},
		{
			name: "multiple literals joined by newline",
			args: []string{"multikey", "--from-literal=KEY1=val1", "--from-literal=KEY2=val2"},
			clientFn: func(doc v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error) {
				if doc.Spec.Data != "val1\nval2" {
					t.Errorf("unexpected data: %q", doc.Spec.Data)
				}
				return kukeonv1.CreateSecretResult{
					Secret:  doc,
					Created: true,
				}, nil
			},
			wantOut: `Secret "multikey" (realm "default", space "", stack "")`,
		},
		{
			name: "from file happy path",
			args: []string{"filesecret", "--from-file=__testdata__/secretvalue.txt"},
			clientFn: func(doc v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error) {
				if doc.Spec.Data != "file-content" {
					t.Errorf("unexpected data: %q", doc.Spec.Data)
				}
				return kukeonv1.CreateSecretResult{
					Secret:  doc,
					Created: true,
				}, nil
			},
			wantOut: `Secret "filesecret" (realm "default", space "", stack "")`,
		},
		{
			name: "scope flags",
			args: []string{"scoped", "--from-literal=VAL", "--realm=myrealm", "--space=myspace", "--stack=mystack"},
			clientFn: func(doc v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error) {
				if doc.Metadata.Realm != "myrealm" {
					t.Errorf("unexpected realm: %q", doc.Metadata.Realm)
				}
				if doc.Metadata.Space != "myspace" {
					t.Errorf("unexpected space: %q", doc.Metadata.Space)
				}
				if doc.Metadata.Stack != "mystack" {
					t.Errorf("unexpected stack: %q", doc.Metadata.Stack)
				}
				return kukeonv1.CreateSecretResult{
					Secret:  doc,
					Created: true,
				}, nil
			},
			wantOut: `Secret "scoped" (realm "myrealm", space "myspace", stack "mystack")`,
		},
		{
			name:    "no source flags rejected",
			args:    []string{"nodata"},
			wantErr: "at least one of --from-literal or --from-file is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := secret.NewSecretCmd()
			out := &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(out)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

			if tt.clientFn != nil {
				fake := &fakeClient{createSecretFn: tt.clientFn}
				ctx = context.WithValue(ctx, secret.MockControllerKey{}, kukeonv1.Client(fake))
			}
			cmd.SetContext(ctx)

			if tt.setup != nil {
				tt.setup(t, cmd)
			}

			cmd.SetArgs(tt.args)
			err := cmd.Execute()

			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if err.Error() != tt.wantErr {
					t.Errorf("expected error %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantOut != "" {
				output := out.String()
				if !contains(output, tt.wantOut) {
					t.Errorf("expected output to contain %q, got %q", tt.wantOut, output)
				}
			}
		})
	}
}

func TestNewSecretCmd_AutocompleteRegistration(t *testing.T) {
	cmd := secret.NewSecretCmd()

	realmFlag := cmd.Flags().Lookup("realm")
	if realmFlag == nil {
		t.Fatal("expected 'realm' flag to exist")
	}

	spaceFlag := cmd.Flags().Lookup("space")
	if spaceFlag == nil {
		t.Fatal("expected 'space' flag to exist")
	}

	stackFlag := cmd.Flags().Lookup("stack")
	if stackFlag == nil {
		t.Fatal("expected 'stack' flag to exist")
	}

	fromLiteralFlag := cmd.Flags().Lookup("from-literal")
	if fromLiteralFlag == nil {
		t.Fatal("expected 'from-literal' flag to exist")
	}

	fromFileFlag := cmd.Flags().Lookup("from-file")
	if fromFileFlag == nil {
		t.Fatal("expected 'from-file' flag to exist")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestMain(m *testing.M) {
	// Create testdata directory with a test secret file
	testdataDir := "__testdata__"
	if err := os.MkdirAll(testdataDir, 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(testdataDir, "secretvalue.txt"), []byte("file-content"), 0o644); err != nil {
		panic(err)
	}

	code := m.Run()

	os.RemoveAll(testdataDir)
	os.Exit(code)
}