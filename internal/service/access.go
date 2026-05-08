package service

import "github.com/Mininglamp-OSS/octo-matter/internal/model"

// MatterAccessChecker verifies if a user can view a matter. sourceChannelID,
// when non-empty, grants read access if it matches the matter's source channel.
type MatterAccessChecker interface {
	CanAccessMatter(matter *model.Matter, userID string, sourceChannelID string) bool
}
