// Package runtime previously held a thin HTTP client to octo-server's
// /v1/internal/bot-tasks endpoint. That dispatch path was removed once
// matter started enqueueing bot tasks via local matter_bot_task instead
// (plan §1 — matter doesn't call fleet/server).
//
// The package is retained only for IsBotUID, which is the heuristic for
// "is this assignee a bot" (uid suffixed with _bot). v1.1 will resolve
// via octo-server /v1/user/info?uid=X { robot: true } and this helper
// can move to a neutral package then.
package runtime

import "strings"

// IsBotUID is the PoC heuristic for "is this assignee a bot". Mints from
// botfather suffix the user_id with "_bot" (see octo-server/modules/botfather/
// command.go::tryCreateBotCore).
func IsBotUID(uid string) bool {
	return strings.HasSuffix(uid, "_bot")
}
