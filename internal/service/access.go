// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"

	"github.com/Mininglamp-OSS/octo-matter/internal/model"
)

// MatterAccessChecker verifies whether any of callerUIDs can view a matter.
//
// channelID, when non-empty, may grant access via matter_channels link ONLY
// after the caller proves channel membership via dmworkim (callerToken
// non-empty). For the bot path (callerToken=="") the channel claim is
// stripped before reaching the repo, so authz must succeed via creator /
// assignee / participant. Defense-in-depth: even a leaked bot token cannot
// pivot through a known/guessed linked channel id.
//
// callerToken is the user's IM auth token (forwarded to dmworkim's channel
// members API). Empty for bot callers — bots authenticate via verify-bot,
// not via the public channel-members endpoint.
type MatterAccessChecker interface {
	CanAccessMatter(ctx context.Context, matter *model.Matter, callerUIDs []string, channelID, callerToken string) (bool, error)
}

// imChecker is the minimal surface MatterService needs from dmworkim.Client.
// Defined here so unit tests can inject a stub without depending on the HTTP
// client.
type imChecker interface {
	IsChannelMember(ctx context.Context, token, channelID string, userIDs []string) (bool, error)
}

// noopIMChecker treats every caller as a non-member, used when no IM client is
// configured (purely for tests / dev environments without dmworkim).
type noopIMChecker struct{}

func (noopIMChecker) IsChannelMember(_ context.Context, _, _ string, _ []string) (bool, error) {
	return false, nil
}
