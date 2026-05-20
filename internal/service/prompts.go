package service

import (
	"context"

	"github.com/Mininglamp-OSS/octo-matter/internal/llm/prompts"
	"github.com/Mininglamp-OSS/octo-matter/internal/llm/promptstore"
)

// defaultPromptStore is the package-level Store used by the extract and
// timeline services when no override is provided. It serves prompts from
// the embed.FS in internal/llm/prompts — editing those .md / .tool.json
// files is the supported way to change LLM prompts.
//
// A future Langfuse-backed Store will compose with this one (Langfuse
// primary, embed as fallback) and be wired via WithPromptStore options
// in cmd/main.go.
var defaultPromptStore promptstore.Store = promptstore.NewEmbed(prompts.FS)

// promptNames are the canonical names referenced by services. Listed here
// (rather than scattered as string literals) so verifyPromptsAvailable can
// fail fast at startup if the embed FS is missing a prompt.
const (
	promptExtractMatter   = "extract_matter"
	promptExtractProgress = "extract_progress"
)

// init validates that every known prompt loads cleanly from the default
// store. A missing or malformed prompt is a programmer error in this repo
// (the files are embedded at build time) and we want it to surface at
// process start, not on the first user request.
//
// context.Background() is intentional: EmbedStore ignores ctx. A remote
// Store (e.g. the planned Langfuse provider) must NOT be assigned to
// defaultPromptStore — wire remote stores via functional options
// (WithExtractPromptStore / WithTimelinePromptStore) so that network calls
// only happen after process init completes.
func init() {
	ctx := context.Background()
	for _, name := range []string{promptExtractMatter, promptExtractProgress} {
		if _, err := defaultPromptStore.Get(ctx, name); err != nil {
			panic("service: default prompt unavailable: " + name + ": " + err.Error())
		}
	}
}
