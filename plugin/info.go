package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// InfoFlag makes a plugin binary print its identity as one JSON object on
// stdout and exit — no handshake, no gRPC, no RACKBILL_PLUGIN cookie needed.
// The publish pipeline reads it from freshly built binaries so the
// distribution catalog can never drift from the code, and an operator can run
// `<binary> -rb-plugin-info` to identify a file on disk.
const InfoFlag = "-rb-plugin-info"

// BinaryInfo is what a plugin binary prints for InfoFlag. Kind, Name,
// DisplayName and Maturity describe the binary's primary kind — the one its
// name carries; Serves lists every kind it serves, primary first (decision
// 118), and is absent from binaries built before bundles existed, which serve
// exactly Kind. Maturity is the *declared* value: consumers normalize it
// exactly as the host does (NormalizeMaturity, with `<kind>-dev` binaries
// forced to dev by name). Contract is the ContractVersion the binary was
// built with — filled in by the SDK, never by the plugin — which the catalog
// carries as the agent floor (decision 117); absent (0) from binaries built
// before it existed.
type BinaryInfo struct {
	Schema      int          `json:"schema"`
	Kind        string       `json:"kind"`
	Name        string       `json:"name"`
	DisplayName string       `json:"display_name"`
	Version     string       `json:"version"`
	Maturity    string       `json:"maturity"`
	Contract    int          `json:"contract,omitempty"`
	Serves      []ServedKind `json:"serves,omitempty"`
}

// ServedKinds is what the binary serves with the pre-bundle shape folded in:
// Serves when it was printed, else the one kind the older fields name.
func (b BinaryInfo) ServedKinds() []ServedKind {
	if len(b.Serves) > 0 {
		return b.Serves
	}
	return []ServedKind{{Kind: b.Kind, Name: b.Name, DisplayName: b.DisplayName, Version: b.Version, Maturity: b.Maturity}}
}

// serveInfo answers InfoFlag before the go-plugin handshake: ServeBundle
// calls it first with the kinds the binary serves and which of them is
// primary. Without the flag it returns immediately and serving proceeds. Each
// kind's Describe is called in-process; a kind that cannot describe itself
// fails the whole answer, because a binary that cannot say what it is does
// not publish (§17.3).
func serveInfo(kinds []served, primary int) {
	if len(os.Args) < 2 || os.Args[1] != InfoFlag {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Primary first: the older single-kind fields mirror Serves[0], so a
	// consumer that knows either shape reads the same identity.
	order := []int{primary}
	for i := range kinds {
		if i != primary {
			order = append(order, i)
		}
	}
	info := BinaryInfo{Schema: 1, Contract: ContractVersion}
	for _, i := range order {
		sk, err := kinds[i].describe(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "describe %s: %v\n", kinds[i].kind, err)
			os.Exit(1)
		}
		info.Serves = append(info.Serves, sk)
	}
	head := info.Serves[0]
	info.Kind, info.Name, info.DisplayName = head.Kind, head.Name, head.DisplayName
	info.Version, info.Maturity = head.Version, head.Maturity
	if err := json.NewEncoder(os.Stdout).Encode(info); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}
