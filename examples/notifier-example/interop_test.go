//go:build python_example

// This test is a Go HOST for the Python plugin, not part of its implementation.
// Build the Python example first, then run go test -tags=python_example here.
package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	sdk "rackbill.com/sdk/plugin"
	"rackbill.com/sdk/plugin/proto"
)

type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Logf("plugin: %s", p)
	return len(p), nil
}

func TestPythonPlugin(t *testing.T) {
	launcher, err := filepath.Abs("dist/notifier-python-example")
	if err != nil {
		t.Fatal(err)
	}
	// RackBill does not pass an activated venv, PYTHONPATH or arbitrary RB_*
	// settings to plugins. Also start outside the example's source directory.
	command := func(args ...string) *exec.Cmd {
		cmd := exec.Command(launcher, args...)
		cmd.Dir = t.TempDir()
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "RACKBILL_LOG_LEVEL=error"}
		return cmd
	}
	output, err := command(sdk.InfoFlag).CombinedOutput()
	if err != nil {
		t.Fatalf("metadata: %v: %s; run build.py first", err, output)
	}
	var metadata sdk.BinaryInfo
	if err := json.Unmarshal(output, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Schema != 1 || metadata.Kind != "notifier" || metadata.Name != "python-example" ||
		metadata.Maturity != sdk.MaturityDev || metadata.Contract != sdk.ContractVersion {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}

	for _, mtls := range []bool{true, false} {
		t.Run(fmt.Sprintf("AutoMTLS=%t", mtls), func(t *testing.T) {
			config := &goplugin.ClientConfig{
				HandshakeConfig: sdk.Handshake,
				Plugins: map[string]goplugin.Plugin{
					sdk.NotifierPluginName: &sdk.NotifierPlugin{},
				},
				Cmd: command(), SkipHostEnv: true,
				AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
				AutoMTLS:         mtls, StartTimeout: 10 * time.Second,
				Logger: hclog.New(&hclog.LoggerOptions{Output: testLogWriter{t}, Level: hclog.Debug}),
			}
			client := goplugin.NewClient(config)
			t.Cleanup(client.Kill)
			rpc, err := client.Client()
			if err != nil {
				t.Fatal(err)
			}
			if err := rpc.Ping(); err != nil {
				t.Fatalf("go-plugin health probe: %v", err)
			}
			raw, err := rpc.Dispense(sdk.NotifierPluginName)
			if err != nil {
				t.Fatal(err)
			}
			notifier := raw.(sdk.Notifier)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			info, err := notifier.Describe(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if info.Name != metadata.Name || info.Version != metadata.Version || info.DisplayName != metadata.DisplayName ||
				len(info.Channels) != 1 || info.Channels[0] != sdk.ChannelEmail || info.SupportsMemberRoles ||
				len(info.ConfigFields) != 1 || info.ConfigFields[0].Key != "simulate" || len(info.ConfigFields[0].Options) != 2 {
				t.Fatalf("unexpected Describe: %+v", info)
			}

			// Interleave tenant settings across actual RPCs; no call may inherit
			// another tenant's outcome, and none may report a fake delivery.
			var calls sync.WaitGroup
			for i := range 24 {
				calls.Go(func() {
					outcome := []string{"", "error", "unknown"}[i%3]
					result, err := notifier.Send(ctx, sdk.SendInput{
						TenantID: fmt.Sprintf("tenant-%d", i), Channel: sdk.ChannelEmail,
						To: "recipient@example.test", Body: "private body",
						Config: map[string]string{"simulate": outcome},
					})
					if outcome == "error" {
						if status.Code(err) != codes.Unavailable {
							t.Errorf("transport failure: %v", err)
						}
						return
					}
					want := "Example only: message delivery is not implemented."
					if outcome == "unknown" {
						want = "Unknown simulated outcome."
					}
					if err != nil || result.Status != sdk.SendFailed || result.ProviderID != "" || result.FailureReason != want {
						t.Errorf("outcome %q: %+v, %v", outcome, result, err)
					}
				})
			}
			calls.Wait()
			result, err := notifier.Send(ctx, sdk.SendInput{Channel: sdk.ChannelSMS})
			if err != nil || result.Status != sdk.SendFailed || result.FailureReason != "This example supports email only." {
				t.Fatalf("unsupported channel: %+v, %v", result, err)
			}

			conn := rpc.(*goplugin.GRPCClient).Conn
			_, err = proto.NewNotifierClient(conn).GetChatMember(ctx, &proto.GetChatMemberRequest{})
			if status.Code(err) != codes.Unimplemented {
				t.Fatalf("optional notifier capability: %v", err)
			}
			_, err = proto.NewBundleClient(conn).DescribeBundle(ctx, &proto.DescribeBundleRequest{})
			if status.Code(err) != codes.Unimplemented {
				t.Fatalf("single-kind bundle probe: %v", err)
			}
			_, err = proto.NewWidgetProviderClient(conn).DescribeWidgets(ctx, &proto.DescribeWidgetsRequest{})
			if status.Code(err) != codes.Unimplemented {
				t.Fatalf("widget probe: %v", err)
			}
			_, err = proto.NewStorageManifestClient(conn).DescribeStorage(ctx, &proto.StorageDescribeRequest{})
			if status.Code(err) != codes.Unimplemented {
				t.Fatalf("storage probe: %v", err)
			}

			if mtls {
				// Server authentication alone is insufficient: a client without
				// the host's certificate must not reach the notifier.
				tlsConfig := config.TLSConfig.Clone()
				tlsConfig.Certificates = nil
				unauthenticated, err := grpc.NewClient(client.ReattachConfig().Addr.String(),
					grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
				if err != nil {
					t.Fatal(err)
				}
				defer unauthenticated.Close()
				probeCtx, probeCancel := context.WithTimeout(ctx, time.Second)
				defer probeCancel()
				_, err = proto.NewNotifierClient(unauthenticated).DescribeNotifier(probeCtx, &proto.DescribeNotifierRequest{})
				if err == nil {
					t.Fatal("TLS client without the host certificate was accepted")
				}
			}

			// Close exercises GRPCController.Shutdown, not just process kill.
			if err := rpc.Close(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for !client.Exited() && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if !client.Exited() {
				t.Fatal("plugin did not exit after graceful shutdown")
			}
		})
	}
}
