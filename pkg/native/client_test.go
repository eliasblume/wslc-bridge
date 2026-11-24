/**********************************************************************
 * Copyright (C) 2026 Red Hat, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 **********************************************************************/

package native

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"wslc-bridge/pkg/process"
)

// fakeRunner is a process.Runner that records wslc.exe calls and answers
// them with canned results.
type fakeRunner struct {
	mu    sync.Mutex
	calls [][]string
	// run answers RunWSLC calls.
	run func(args []string) (process.Result, error)
	// stream answers RunWSLCStreaming calls.
	stream func(args []string, stdout, stderr io.Writer) (int, error)
}

var _ process.Runner = (*fakeRunner)(nil)

func (f *fakeRunner) record(args []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
}

func (f *fakeRunner) recorded() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeRunner) RunWSLC(ctx context.Context, args ...string) (process.Result, error) {
	f.record(args)
	if _, ok := ctx.Deadline(); !ok {
		return process.Result{}, errors.New("wslc.exe called without a timeout")
	}
	return f.run(args)
}

func (f *fakeRunner) RunWSL(context.Context, ...string) (process.Result, error) {
	return process.Result{}, errors.New("unexpected wsl.exe call")
}

func (f *fakeRunner) RunWSLCStreaming(_ context.Context, stdout, stderr io.Writer, args ...string) (int, error) {
	f.record(args)
	return f.stream(args, stdout, stderr)
}

func respondWith(output string) func([]string) (process.Result, error) {
	return func([]string) (process.Result, error) {
		return process.Result{Stdout: []byte(output)}, nil
	}
}

func commandFailure(code int) error {
	return &process.CommandError{Name: "wslc.exe", ExitCode: code, Stderr: fmt.Sprintf("failed with %d", code)}
}

func assertCalls(t *testing.T, runner *fakeRunner, want ...[]string) {
	t.Helper()
	if got := runner.recorded(); !reflect.DeepEqual(got, want) {
		t.Fatalf("wslc.exe calls = %q, want %q", got, want)
	}
}

func assertExitCode(t *testing.T, err error, code int) {
	t.Helper()
	var commandError *process.CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("error = %v, want *process.CommandError", err)
	}
	if commandError.ExitCode != code {
		t.Fatalf("exit code = %d, want %d", commandError.ExitCode, code)
	}
}

func TestDecodeContainerListSupportsArrayAndJSONLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		data             string
		wantFirstPortKey string
		wantHostPort     string
	}{
		{
			name:             "2.9.3 array",
			data:             `[{"Id":"container-a","State":2,"Ports":[{"HostPort":49152,"ContainerPort":80,"Protocol":6,"BindingAddress":"127.0.0.1"}]},{"Id":"container-b","State":3,"Ports":[]}]`,
			wantFirstPortKey: "80/tcp",
			wantHostPort:     "49152",
		},
		{
			name:             "newer JSON lines",
			data:             "{\"ID\":\"container-a\",\"State\":\"running\",\"Ports\":\"127.0.0.1:49152->80/tcp, [::1]:49153->81/udp\"}\n{\"ID\":\"container-b\",\"State\":\"exited\",\"Ports\":\"\"}\n",
			wantFirstPortKey: "80/tcp",
			wantHostPort:     "49152",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			items, err := DecodeContainerList([]byte(test.data))
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 2 || items[0].ID != "container-a" {
				t.Fatalf("DecodeContainerList() = %#v", items)
			}
			binding := items[0].Ports[test.wantFirstPortKey][0]
			if binding.HostPort != test.wantHostPort {
				t.Fatalf("first port binding = %#v, want host port %s", binding, test.wantHostPort)
			}
		})
	}
}

func TestListContainersDecodesCommandOutput(t *testing.T) {
	t.Parallel()
	const fullID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	output := `{"ID":"` + fullID + `","State":"running","Ports":"127.0.0.1:49152->80/tcp"}
{"ID":"container-b","State":"exited","Ports":""}`
	runner := &fakeRunner{run: respondWith(output)}
	items, err := newClient(runner).ListContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, runner, []string{"container", "list", "--all", "--no-trunc", "--format", "json"})
	if len(items) != 2 || items[0].ID != fullID || items[0].State != "running" {
		t.Fatalf("ListContainers() = %#v", items)
	}
	binding := items[0].Ports["80/tcp"][0]
	if binding.HostIP != "127.0.0.1" || binding.HostPort != "49152" {
		t.Fatalf("port binding = %#v, want 127.0.0.1:49152", binding)
	}
}

func TestListContainersPropagatesCommandFailure(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{run: func([]string) (process.Result, error) { return process.Result{}, commandFailure(3) }}
	_, err := newClient(runner).ListContainers(context.Background())
	assertExitCode(t, err, 3)
}

func TestCreateContainerReturnsLastNonEmptyOutputLine(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{run: respondWith("creating container\ncontainer-id\n\n")}
	id, err := newClient(runner).CreateContainer(context.Background(), []string{"container", "create", "alpine"})
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, runner, []string{"container", "create", "alpine"})
	if id != "container-id" {
		t.Fatalf("CreateContainer() = %q, want \"container-id\"", id)
	}
}

func TestCreateContainerRejectsEmptyOutput(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{run: respondWith("\n")}
	_, err := newClient(runner).CreateContainer(context.Background(), []string{"container", "create", "alpine"})
	if err == nil || !strings.Contains(err.Error(), "empty container ID") {
		t.Fatalf("CreateContainer() error = %v, want an empty container ID error", err)
	}
}

func TestPullImageSucceeds(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{run: respondWith("")}
	if err := newClient(runner).PullImage(context.Background(), "alpine:3.20"); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, runner, []string{"image", "pull", "alpine:3.20"})
}

func TestPullImagePropagatesCommandFailure(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{run: func([]string) (process.Result, error) { return process.Result{}, commandFailure(9) }}
	err := newClient(runner).PullImage(context.Background(), "fail:latest")
	assertExitCode(t, err, 9)
}

func TestExecContainerStreamsCommandOutput(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{stream: func(_ []string, stdout, _ io.Writer) (int, error) {
		_, _ = io.WriteString(stdout, "exec-output\n")
		return 0, nil
	}}
	var stdout, stderr bytes.Buffer
	code, err := newClient(runner).ExecContainer(context.Background(), "container-id", []string{"ls", "-l"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, runner, []string{"container", "exec", "container-id", "ls", "-l"})
	if code != 0 || stdout.String() != "exec-output\n" || stderr.Len() != 0 {
		t.Fatalf("ExecContainer() = (%d, %q, %q), want streamed output", code, stdout.String(), stderr.String())
	}
}

func TestExecContainerPropagatesExitCode(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{stream: func([]string, io.Writer, io.Writer) (int, error) { return 8, commandFailure(8) }}
	var stdout, stderr bytes.Buffer
	code, err := newClient(runner).ExecContainer(context.Background(), "failing-container", []string{"ls"}, &stdout, &stderr)
	assertExitCode(t, err, 8)
	if code != 8 {
		t.Fatalf("ExecContainer() exit code = %d, want 8", code)
	}
}

func TestCreateNetwork(t *testing.T) {
	t.Parallel()
	createArgs := []string{"network", "create", "testnet"}
	tests := []struct {
		name        string
		createErr   error
		inspect     string
		inspectErr  error
		wantID      string
		wantCode    int
		wantMessage string
		wantCalls   [][]string
	}{
		{name: "returns inspected ID", inspect: `[{"ID":"net-42"}]`, wantID: "net-42"},
		{name: "accepts legacy Id field", inspect: `[{"Id":"net-legacy"}]`, wantID: "net-legacy"},
		{name: "propagates create failure", createErr: commandFailure(6), wantCode: 6, wantCalls: [][]string{createArgs}},
		{name: "propagates inspect failure", inspectErr: commandFailure(5), wantCode: 5},
		{name: "rejects undecodable inspect output", inspect: "not-json", wantMessage: "decode wslc network inspect"},
		{name: "rejects empty inspect output", inspect: "[]", wantMessage: "no network ID"},
		{name: "rejects inspect output without ID", inspect: `[{}]`, wantMessage: "no network ID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{run: func(args []string) (process.Result, error) {
				if args[1] == "create" {
					return process.Result{}, test.createErr
				}
				return process.Result{Stdout: []byte(test.inspect)}, test.inspectErr
			}}
			id, err := newClient(runner).CreateNetwork(context.Background(), createArgs, "testnet")
			wantCalls := test.wantCalls
			if wantCalls == nil {
				wantCalls = [][]string{createArgs, {"network", "inspect", "testnet"}}
			}
			assertCalls(t, runner, wantCalls...)
			switch {
			case test.wantCode != 0:
				assertExitCode(t, err, test.wantCode)
			case test.wantMessage != "":
				if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
					t.Fatalf("CreateNetwork() error = %v, want it to contain %q", err, test.wantMessage)
				}
			case err != nil:
				t.Fatal(err)
			case id != test.wantID:
				t.Fatalf("CreateNetwork() = %q, want %q", id, test.wantID)
			}
		})
	}
}
