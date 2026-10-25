// Command notifier-example demonstrates the scaffolding for a RackBill plugin.
// It simulates failures; replace Send with a real provider integration.
package main

import (
	"context"
	"errors"

	sdk "rackbill.com/sdk/plugin"
)

// One instance can serve concurrent calls for multiple tenants. Keep request
// configuration in SendInput rather than storing it on the plugin instance.
type exampleNotifier struct{}

var _ sdk.Notifier = exampleNotifier{}

// Describe declares the plugin identity, capabilities and configuration form.
// It should describe the plugin without contacting an external provider.
func (exampleNotifier) Describe(context.Context) (sdk.NotifierInfo, error) {
	return sdk.NotifierInfo{
		Name:        "example",
		DisplayName: "Example notifier (simulation)",
		Version:     "0.1.0", // Your plugin's version, independent of the SDK version.
		Maturity:    sdk.MaturityDev,
		Channels:    []string{sdk.ChannelEmail},
		ConfigFields: []sdk.ConfigField{{
			Key: "simulate", Label: "Simulated outcome", Type: "select",
			Options: []sdk.ConfigOption{
				{Value: "", Label: "Provider rejection", Description: "Return a failed result without sending a message."},
				{Value: "error", Label: "Transport error", Description: "Return an error without contacting a provider."},
			},
		}},
	}, nil
}

func (exampleNotifier) Send(ctx context.Context, in sdk.SendInput) (sdk.SendResult, error) {
	if err := ctx.Err(); err != nil {
		return sdk.SendResult{}, err
	}
	if in.Channel != sdk.ChannelEmail {
		return sdk.SendResult{Status: sdk.SendFailed, FailureReason: "This example supports email only."}, nil
	}

	// Use sdk.Log for diagnostics, keeping credentials and message contents out
	// of logs. Stdout belongs to the plugin protocol.
	sdk.Log.Debug("Simulating notification delivery", "channel", in.Channel)

	// Config contains this tenant's settings on each call. A real Send would
	// pass ctx to its provider request and return the provider's delivery ID.
	switch in.Config["simulate"] {
	case "":
		// A declared result represents a provider rejection.
		return sdk.SendResult{Status: sdk.SendFailed, FailureReason: "Example only: message delivery is not implemented."}, nil
	case "error":
		// A returned error represents a transport failure.
		return sdk.SendResult{}, errors.New("example only: simulated transport failure")
	default:
		return sdk.SendResult{Status: sdk.SendFailed, FailureReason: "Unknown simulated outcome."}, nil
	}
}

func main() {
	// The SDK supplies the handshake, gRPC server and -rb-plugin-info flag.
	sdk.ServeNotifier(exampleNotifier{})
}
