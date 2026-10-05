// Package opencodeext is piggery's opencode plugin as shipped in the binary: `piggery setup opencode`
// unpacks it under ~/.piggery/plugins/opencode, and opencode loads its entry as a plugin, for the
// sessions the Human opens (the TUI) and for the workers piggery spawns (`opencode serve`) alike.
//
// The entry (opencode/index.mjs) is opencode's own; it reuses pi's adapter, client, render and
// tools.json by import (../pi/<file>), so there is one copy of each in the repository. An unpacked
// copy has the same shape as the repository, so the imports resolve in both:
//
//	opencode/<module>  this directory's modules
//	pi/<shared file>   the files of extensions/pi that opencode/index.mjs imports
package opencodeext

import (
	"embed"
	"fmt"

	piext "github.com/sting8k/piggery/extensions/pi"
)

//go:embed index.mjs bridge.mjs records.mjs
var files embed.FS

// Entry is the path, from the root of an unpacked copy, of the file opencode loads.
const Entry = "opencode/index.mjs"

// Own are this directory's modules, at opencode/<name> in a copy.
var Own = []string{"index.mjs", "bridge.mjs", "records.mjs"}

// Shared are the files of extensions/pi that opencode/index.mjs imports, at pi/<name> in a copy. A
// test keeps this list equal to the imports.
var Shared = []string{"adapter.mjs", "client.mjs", "render.mjs", "tools.json"}

// Tree is the files of a copy by slash path: the plugin's modules and the shared files.
func Tree() (map[string][]byte, error) {
	tree := map[string][]byte{}
	for _, name := range Own {
		b, err := files.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("opencodeext: %s: %w", name, err)
		}
		tree["opencode/"+name] = b
	}
	for _, name := range Shared {
		b, err := piext.Files.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("opencodeext: shared file %s: %w", name, err)
		}
		tree["pi/"+name] = b
	}
	return tree, nil
}
