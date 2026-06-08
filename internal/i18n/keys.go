// Package i18n is octo-matter's lightweight localization layer. It wraps
// go-i18n with an embedded TOML message catalog (zh-CN / en-US), HTTP language
// negotiation aligned with octo-server (X-Octo-Lang > ?lang > cookie >
// user.language > Accept-Language > default), and a Gin-aware error responder.
//
// Message IDs are stable keys under err.* / notify.* segments. en-US is the
// source language; zh-CN is the translation. Missing translations fall back to
// the source string, then to the key itself.
package i18n

// Error message keys. Grouped by segment; the string value IS the go-i18n
// MessageID and must match a [section] header in locales/active.*.toml.
const (
	// Generic / transport
	KeyNotFound        = "err.common.not_found"
	KeyForbidden       = "err.common.forbidden"
	KeyInternal        = "err.common.internal"
	KeyPayloadTooLarge = "err.common.payload_too_large"

	// Auth + space (auth/space middleware)
	KeyAuthMissingToken    = "err.auth.missing_token"
	KeyAuthUnavailable     = "err.auth.unavailable"
	KeyAuthInvalidToken    = "err.auth.invalid_token"
	KeyAuthInvalidBotToken = "err.auth.invalid_bot_token"
	KeyAuthParseFailed     = "err.auth.parse_failed"
	KeySpaceMissingHeader  = "err.space.missing_header"
	KeySpaceForbidden      = "err.space.forbidden"
	KeySpaceUnavailable    = "err.space.unavailable"

	// Validation
	KeyInvalidID           = "err.validation.invalid_id"
	KeyInvalidEntryID      = "err.validation.invalid_entry_id"
	KeyInvalidCursor       = "err.validation.invalid_cursor"
	KeyInvalidRequest      = "err.validation.invalid_request"
	KeyMatterIDMismatch    = "err.validation.matter_id_mismatch"
	KeyContentRequired     = "err.validation.content_required"
	KeyContentTooLong      = "err.validation.content_too_long"
	KeyTooManyAttachments  = "err.validation.too_many_attachments"
	KeyFileURLRequired     = "err.validation.file_url_required"
	KeyAttachmentTooLarge  = "err.validation.attachment_too_large"
	KeyParticipantUIDReq   = "err.validation.participant_uid_required"
	KeyChannelIDRequired   = "err.validation.channel_id_required"
	KeyChannelTypeInvalid  = "err.validation.channel_type_invalid"
	KeyCreatorUIDRequired  = "err.validation.creator_uid_required"
	KeyMsgsRequired        = "err.validation.msgs_required"
	KeyMsgsLimit           = "err.validation.msgs_limit"       // params: Limit
	KeyMessageIDLimit      = "err.validation.message_id_limit" // params: Index, Limit
	KeyDeadlineFormat      = "err.validation.deadline_format"
	KeyRemindAtFormat      = "err.validation.remind_at_format"
	KeyStatusInvalid       = "err.validation.status_invalid"
	KeyAssigneeUIDRequired = "err.validation.assignee_uid_required"
	KeyCallerIdentityReq   = "err.validation.caller_identity_required"
	KeyTransitionArchived  = "err.validation.transition_archived"

	// Forbidden
	KeyMatterAccess              = "err.forbidden.matter_access"
	KeyMatterView                = "err.forbidden.matter_view"
	KeyParticipantNotAuthorized  = "err.forbidden.participant_not_authorized"
	KeyCreatorUIDNotAuthorized   = "err.forbidden.creator_uid_not_authorized"
	KeyBotLinkChannel            = "err.forbidden.bot_link_channel"
	KeyBotLinkExisting           = "err.forbidden.bot_link_existing"
	KeyNotChannelMember          = "err.forbidden.not_channel_member"
	KeyOnlyCreatorArchive        = "err.forbidden.only_creator_archive"
	KeyOnlyCreatorOrAssigneeStat = "err.forbidden.only_creator_or_assignee_status"

	// Not found
	KeyMatterNotFound   = "err.notfound.matter"
	KeyAssigneeNotFound = "err.notfound.assignee"

	// Upstream / rate limit / conflict
	KeyUpstream          = "err.upstream.generic"
	KeyChannelMembership = "err.upstream.channel_membership"
	KeyRateLimited       = "err.ratelimited.generic"
	KeyRateLimitCooldown = "err.ratelimited.cooldown" // params: Cooldown
	KeyDuplicateAssignee = "err.conflict.duplicate_assignee"

	// LLM
	KeyLLMEmptyExtraction = "err.llm.empty_extraction"
	KeyLLMUpstream        = "err.llm.upstream"
)

// Notification message keys (rendered for the default-language fallback string;
// IM localizes per-recipient from the same key + params).
const (
	KeyNotifyMatterCreated      = "notify.matter_created"       // params: Title, Actor
	KeyNotifyStatusChanged      = "notify.status_changed"       // params: Title, Actor, Action
	KeyNotifyAssigneeAdded      = "notify.assignee_added"       // params: Title, Actor
	KeyNotifyTimelineEntryAdded = "notify.timeline_entry_added" // params: Title, Actor

	KeyNotifyActionDone     = "notify.action.done"
	KeyNotifyActionArchived = "notify.action.archived"
	KeyNotifyActionOpen     = "notify.action.open"
	KeyNotifyActionUpdated  = "notify.action.updated"
)
