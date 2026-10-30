// Package ecosystem holds what stackfork knows per language ecosystem:
// which manifest a project has, which parts of it the startup plan depends
// on, and (as more of learn moves here) how its dev server, watcher,
// install and seed conventions look. learn and knowledge stay generic and
// ask the ecosystem that recognised the project. JS/TS (Nuxt included) is
// the first one; Go, Python and others are one file each.
package ecosystem

import "github.com/pedropinheiro75/stackfork/internal/state"

// Ecosystem is one language ecosystem.
type Ecosystem interface {
	// Name is the short id recorded in knowledge and shown in the dashboard.
	Name() string
	// Detect says whether root is a project of this ecosystem.
	Detect(root string) bool
	// InputHash hashes a scan input this ecosystem owns, reading only the
	// parts the startup plan depends on. claimed is false when the path is
	// not this ecosystem's manifest.
	InputHash(path string) (hash string, claimed bool, err error)
}

// All lists the supported ecosystems in detection order.
var All = []Ecosystem{JS{}}

// Of returns the ecosystem that recognises root, or nil.
func Of(root string) Ecosystem {
	for _, e := range All {
		if e.Detect(root) {
			return e
		}
	}
	return nil
}

// InputHash is the content hash recorded for a scan input and compared
// later by the stale check. The ecosystem that owns the file decides what
// counts; every other file is hashed whole.
func InputHash(path string) (string, error) {
	for _, e := range All {
		if h, ok, err := e.InputHash(path); ok || err != nil {
			return h, err
		}
	}
	return state.SHA256File(path)
}
