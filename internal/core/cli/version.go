package cli

import "fmt"

// Version is the release this binary was built from. The release workflow sets
// it with -ldflags -X; a build from a working tree keeps the placeholder.
var Version = "dev"

// RunVersion prints the release, so a running deployment can say what it is.
func RunVersion([]string) { fmt.Println(Version) }
