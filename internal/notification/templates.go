package notification

import "github.com/Mininglamp-OSS/octo-matter/internal/i18n"

// actionKey maps a matter status to the i18n key for its notification verb.
// Used by OctoNotifier to build both the structured payload (action_key) and
// the default-language fallback message.
func actionKey(newStatus string) string {
	switch newStatus {
	case "done":
		return i18n.KeyNotifyActionDone
	case "archived":
		return i18n.KeyNotifyActionArchived
	case "open":
		return i18n.KeyNotifyActionOpen
	default:
		return i18n.KeyNotifyActionUpdated
	}
}
