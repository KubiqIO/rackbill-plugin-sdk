package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	_ "kubiq.io/rackbill/sdk/plugin/proto"
)

// pinnedContractSurface is the fingerprint of the contract surface at
// ContractVersion: every message, field and RPC the wire carries, and every
// exported string constant this package declares (the vocabularies the wire
// carries as strings). TestContractSurfaceIsPinned fails when the surface
// changes, and stays failed until whoever changed it decides whether an
// older agent could still run and present a plugin using the change — re-pin
// the fingerprint if so, bump ContractVersion (and add a Contracts row)
// first if not. The failure message prints the new value.
// Fractional accounting tax fields are additive: updated plugins accept old
// hosts' integer rates; clients gate fractional exports on the SDK capability.
const pinnedContractSurface = "111927df668a2a252bc2c6c9d17805eefd32bfc50dcbc1a5921ef9f8568997de"

const protoPackage = "rackbill.plugin.v1"

func TestContractsAreConsecutive(t *testing.T) {
	if len(Contracts) != ContractVersion {
		t.Fatalf("Contracts has %d rows but ContractVersion is %d — every version needs its row", len(Contracts), ContractVersion)
	}
	semver := regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	for i, c := range Contracts {
		if c.Version != i+1 {
			t.Errorf("Contracts[%d].Version = %d, want %d", i, c.Version, i+1)
		}
		if !semver.MatchString(c.Agent) {
			t.Errorf("contract %d names agent %q, want a release version", c.Version, c.Agent)
		}
		if strings.TrimSpace(c.Note) == "" {
			t.Errorf("contract %d has no note", c.Version)
		}
	}
	if got := AgentForContract(ContractVersion); got != Contracts[len(Contracts)-1].Agent {
		t.Errorf("AgentForContract(%d) = %q", ContractVersion, got)
	}
	if got := AgentForContract(ContractVersion + 1); got != "" {
		t.Errorf("AgentForContract(unknown) = %q, want empty", got)
	}
}

func TestContractSurfaceIsPinned(t *testing.T) {
	got := contractSurface(t)
	if got != pinnedContractSurface {
		t.Fatalf("the plugin contract surface changed (fingerprint %s, pinned %s).\n"+
			"Decide: could an agent built before this change still run and present a plugin that uses it?\n"+
			"  - yes: re-pin pinnedContractSurface in contract_test.go to the new value\n"+
			"  - no:  bump ContractVersion in contract.go, add its Contracts row, then re-pin\n"+
			"The floor the marketplace enforces (decision 117) is only as honest as this answer.",
			got, pinnedContractSurface)
	}
}

// contractSurface hashes what the wire and the vocabularies look like. The
// wire comes from the descriptors the generated code registers; the
// vocabularies from the exported string constants declared in this package's
// source, so a new scope, type or capability name changes the fingerprint
// even though it never touches a proto.
func contractSurface(t *testing.T) string {
	t.Helper()
	var lines []string
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if string(fd.Package()) != protoPackage {
			return true
		}
		walkMessages(fd.Messages(), &lines)
		svcs := fd.Services()
		for i := 0; i < svcs.Len(); i++ {
			s := svcs.Get(i)
			ms := s.Methods()
			for j := 0; j < ms.Len(); j++ {
				m := ms.Get(j)
				lines = append(lines, fmt.Sprintf("rpc %s.%s(%s) %s stream=%v/%v",
					s.FullName(), m.Name(), m.Input().FullName(), m.Output().FullName(),
					m.IsStreamingClient(), m.IsStreamingServer()))
			}
		}
		return true
	})
	if len(lines) == 0 {
		t.Fatalf("no %s descriptors registered — is the proto package imported?", protoPackage)
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				for _, spec := range gd.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if !name.IsExported() {
							continue
						}
						if i < len(vs.Values) {
							if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
								lines = append(lines, "const "+name.Name+"="+lit.Value)
								continue
							}
						}
						lines = append(lines, "const "+name.Name)
					}
				}
			}
		}
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func walkMessages(msgs protoreflect.MessageDescriptors, lines *[]string) {
	for i := 0; i < msgs.Len(); i++ {
		md := msgs.Get(i)
		fields := md.Fields()
		for j := 0; j < fields.Len(); j++ {
			f := fields.Get(j)
			typ := f.Kind().String()
			if f.Message() != nil {
				typ = string(f.Message().FullName())
			}
			if f.IsMap() {
				typ = "map<" + f.MapKey().Kind().String() + "," + f.MapValue().Kind().String() + ">"
			}
			*lines = append(*lines, fmt.Sprintf("field %s.%s=%d %s %s", md.FullName(), f.Name(), f.Number(), f.Cardinality(), typ))
		}
		walkMessages(md.Messages(), lines)
	}
}
