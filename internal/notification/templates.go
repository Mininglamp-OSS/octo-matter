package notification

import "github.com/Mininglamp-OSS/octo-matter/internal/i18n"

// actionKey maps a matter status to the i18n key for its notification verb.
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

// The *Msg helpers render the default-language fallback string carried in the
// notify payload's `message` field. The recipient-specific localization is the
// IM server's job (it renders message_key + params per recipient); these
// fallbacks keep push working before/if IM has not adopted the key.

func matterCreatedMsg(title, actorName string) string {
	return i18n.Localize(i18n.DefaultLanguage(), i18n.KeyNotifyMatterCreated,
		map[string]any{"Title": title, "Actor": actorName})
}

func statusChangedMsg(title, actorName, newStatus string) string {
	action := i18n.Localize(i18n.DefaultLanguage(), actionKey(newStatus), nil)
	return i18n.Localize(i18n.DefaultLanguage(), i18n.KeyNotifyStatusChanged,
		map[string]any{"Title": title, "Actor": actorName, "Action": action})
}

func assigneeAddedMsg(title, actorName string) string {
	return i18n.Localize(i18n.DefaultLanguage(), i18n.KeyNotifyAssigneeAdded,
		map[string]any{"Title": title, "Actor": actorName})
}

func timelineEntryAddedMsg(title, actorName string) string {
	return i18n.Localize(i18n.DefaultLanguage(), i18n.KeyNotifyTimelineEntryAdded,
		map[string]any{"Title": title, "Actor": actorName})
}
