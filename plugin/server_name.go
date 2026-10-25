package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// DefaultServerName returns a readable, DNS-safe default for a non-empty
// RackBill service ID. Three words make it easy to recognize; a 12-hex suffix
// keeps name collisions rare even in large fleets. The name is stable across
// retries, restarts and plugin kinds. It is not a credential or an ownership
// proof: use the full service ID in a provider's external ID or metadata when
// available, and retain the provider's resource identity in ExternalRef.
//
// This mapping is permanent: changing the hash, word order or vocabulary would
// break retry lookups for providers that must use the hostname as their key.
func DefaultServerName(serviceID string) string {
	if serviceID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("rackbill-server-name-v1:" + serviceID))
	return serverNameTraits[int(sum[0])%len(serverNameTraits)] + "-" +
		serverNameColors[int(sum[1])%len(serverNameColors)] + "-" +
		serverNameAnimals[int(sum[2])%len(serverNameAnimals)] + "-" + hex.EncodeToString(sum[3:9])
}

// Each list has 64 entries, so byte selection is unbiased. Keep these lists
// and their order frozen; callers may use the resulting name for adoption.
var serverNameTraits = strings.Fields(`
able agile airy alert ample apt bold brave
bright brisk calm candid clever cool cozy crisp
deft eager even fair fast fine firm fond
free fresh gentle glad grand happy hardy keen
kind lively lofty lucky merry mild neat nimble
noble proud quick quiet ready regal sage serene
sharp sleek smart snug solid spry steady still
stout sunny sure swift tidy trusty warm wise
`)

var serverNameColors = strings.Fields(`
amber aqua ash auburn azure beige berry birch
blue brass bronze cedar cherry chestnut clay cloud
cobalt copper coral cream cyan dusk ebony fern
flax frost garnet gold gray green hazel honey
indigo ink ivory jade jet khaki lilac lime
linen maple mint moss navy oat ochre olive
opal peach pearl pine plum quartz reed rose
ruby russet sand scarlet silver slate teal violet
`)

var serverNameAnimals = strings.Fields(`
alpaca badger bear beaver bison bobcat camel cardinal
caribou cheetah cobra condor cougar crane deer dolphin
dove eagle egret elk falcon ferret finch fox
gecko gibbon goose grouse hare hawk heron ibex
iguana jaguar jay kestrel koala lark lemur lion
lynx marten mink moose newt orca osprey otter
owl panda parrot puffin quail raven robin seal
shark sparrow stork swift tiger turtle whale wren
`)
