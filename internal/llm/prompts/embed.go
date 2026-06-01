// Package prompts exports the embedded prompt and tool-schema files that
// back the default EmbedStore. Edit the .md / .tool.json files in this
// directory to change a prompt — no code changes required; the embed.FS
// re-bakes on the next build.
package prompts

import "embed"

//go:embed *.md *.tool.json
var FS embed.FS
