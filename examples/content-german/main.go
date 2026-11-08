// Command content-german demonstrates a portal translation pack.
// The embedded JSON is a partial sample, not a complete German portal.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	sdk "rackbill.com/sdk/plugin"
)

//go:embed de.json
var germanJSON []byte

type germanContent struct{}

var _ sdk.ContentProvider = germanContent{}

// Describe returns static content, without contacting a provider or receiving
// tenant credentials. Decode a fresh map so concurrent calls never share edits.
func (germanContent) Describe(context.Context) (sdk.ContentInfo, error) {
	var values map[string]string
	if err := json.Unmarshal(germanJSON, &values); err != nil {
		return sdk.ContentInfo{}, fmt.Errorf("decode German content: %w", err)
	}
	return sdk.ContentInfo{
		Name:        "german-example",
		DisplayName: "German portal content example",
		Version:     "0.1.0",
		Maturity:    sdk.MaturityAlpha,
		Packs: []sdk.ContentPack{{
			ID:          "german",
			DisplayName: "German (partial example)",
			Locale:      "de",
			Values:      values,
		}},
	}, nil
}

func main() {
	sdk.ServeContentProvider(germanContent{})
}
