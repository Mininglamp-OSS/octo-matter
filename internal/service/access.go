package service

import "github.com/Mininglamp-OSS/octo-matter/internal/model"

// MatterAccessChecker verifies whether any of callerUIDs can view a matter.
// sourceChannelID, when non-empty, grants read access if that channel is
// linked to the matter via matter_channels.
type MatterAccessChecker interface {
	CanAccessMatter(matter *model.Matter, callerUIDs []string, sourceChannelID string) (bool, error)
}
