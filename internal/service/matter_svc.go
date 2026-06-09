package service

import (
	"context"
	"log"
	"time"

	"github.com/Mininglamp-OSS/octo-matter/internal/apperr"
	"github.com/Mininglamp-OSS/octo-matter/internal/i18n"
	"github.com/Mininglamp-OSS/octo-matter/internal/model"
	"github.com/Mininglamp-OSS/octo-matter/internal/repository"
)

// matterStore is the narrow MatterRepo surface MatterService depends on.
type matterStore interface {
	Create(ctx context.Context, matter *model.Matter) error
	GetByID(ctx context.Context, id, spaceID string) (*model.Matter, error)
	ListBySpace(ctx context.Context, spaceID string, filter repository.MatterFilter) ([]*model.Matter, bool, error)
	Update(ctx context.Context, matter *model.Matter) error
	UpdateStatus(ctx context.Context, id, spaceID, status string) error
	SoftDelete(ctx context.Context, id, spaceID string) error
	HasAccess(ctx context.Context, matterID string, callerUIDs []string, channelID string) (bool, error)
}

type assigneeStore interface {
	Create(ctx context.Context, a *model.MatterAssignee) error
	Delete(ctx context.Context, matterID, userID string) error
	ListByMatter(ctx context.Context, matterID string) ([]*model.MatterAssignee, error)
	ListByMatterIDs(ctx context.Context, matterIDs []string) ([]*model.MatterAssignee, error)
	IsAssignee(ctx context.Context, matterID, userID string) (bool, error)
	IsAssigneeAny(ctx context.Context, matterID string, userIDs []string) (bool, error)
}

type participantStore interface {
	Upsert(ctx context.Context, matterID, userID string) error
	IsParticipantAny(ctx context.Context, matterID string, userIDs []string) (bool, error)
	ListUserIDs(ctx context.Context, matterID string) ([]string, error)
}

type channelStore interface {
	Create(ctx context.Context, mc *model.MatterChannel) error
	Delete(ctx context.Context, matterID, channelID string) error
	IsLinkedChannel(ctx context.Context, matterID, channelID string) (bool, error)
	ListByMatter(ctx context.Context, matterID string) ([]*model.MatterChannel, error)
}

// txRunner runs a closure against a bundle of tx-bound repos.
type txRunner interface {
	Do(ctx context.Context, fn func(r *repository.TxRepos) error) error
}

type MatterService struct {
	matterRepo      matterStore
	assigneeRepo    assigneeStore
	participantRepo participantStore
	channelRepo     channelStore
	activity        activityStore
	tx              txRunner
	im              imChecker
}

// NewMatterService wires the matter service. im may be nil — in that case all
// channel-membership checks are skipped (callers behave as if every user is
// NOT a channel member, which means channel-link access is never granted via
// caller-supplied channelID; assignee/participant/creator paths still work).
func NewMatterService(
	matterRepo matterStore,
	assigneeRepo assigneeStore,
	participantRepo participantStore,
	channelRepo channelStore,
	activity activityStore,
	tx txRunner,
	im imChecker,
) *MatterService {
	if im == nil {
		im = noopIMChecker{}
	}
	return &MatterService{
		matterRepo:      matterRepo,
		assigneeRepo:    assigneeRepo,
		participantRepo: participantRepo,
		channelRepo:     channelRepo,
		activity:        activity,
		tx:              tx,
		im:              im,
	}
}

// recordActivity runs ActivityRepo.Record best-effort — a failure is logged
// but never returned to the caller. Safe to pass a nil store.
func recordActivity(ctx context.Context, store activityStore, matterID, actorID, action string, detail interface{}) {
	if store == nil {
		return
	}
	if err := store.Record(ctx, matterID, actorID, action, detail); err != nil {
		log.Printf("[WARN] activity record failed matter=%s action=%s: %v", matterID, action, err)
	}
}

// CreateMatterWithAssignees creates the matter, auto-links its source channel
// (GAP #13) and inserts all initial assignees in a single transaction.
func (s *MatterService) CreateMatterWithAssignees(ctx context.Context, matter *model.Matter, assigneeIDs []string) (*MatterDetail, error) {
	if matter.Status == "" {
		matter.Status = model.MatterStatusOpen
	}
	var created []*model.MatterAssignee
	err := s.tx.Do(ctx, func(r *repository.TxRepos) error {
		if err := r.Matter.Create(ctx, matter); err != nil {
			return err
		}

		// GAP #13: auto-link the originating channel so later members can still access it.
		if matter.SourceChannelID != nil && *matter.SourceChannelID != "" {
			chType := uint8(0)
			if matter.SourceChannelType != nil {
				chType = *matter.SourceChannelType
			}
			mc := &model.MatterChannel{
				MatterID:    matter.ID,
				ChannelID:   *matter.SourceChannelID,
				ChannelType: chType,
				ChannelName: matter.SourceName,
				LinkedBy:    matter.CreatorID,
			}
			if err := r.MatterChannel.Create(ctx, mc); err != nil {
				return err
			}
		}

		created = make([]*model.MatterAssignee, 0, len(assigneeIDs))
		for _, aid := range assigneeIDs {
			a := &model.MatterAssignee{
				MatterID: matter.ID,
				UserID:   aid,
			}
			if err := r.Assignee.Create(ctx, a); err != nil {
				return err
			}
			created = append(created, a)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	recordActivity(ctx, s.activity, matter.ID, matter.CreatorID, "created", map[string]interface{}{})
	return &MatterDetail{
		Matter:    matter,
		Assignees: created,
	}, nil
}

// MatterListItem is one entry in the list response. It embeds *model.Matter
// (so the response shape stays identical to the previous bare-matter list) and
// adds the matter's assignees to spare the client a per-matter detail call.
// Assignees is always a non-nil slice — empty when the matter has no
// assignees — so JSON consumers get [] rather than null.
type MatterListItem struct {
	*model.Matter
	Assignees []*model.MatterAssignee `json:"assignees"`
}

type MatterListResult struct {
	Items      []*MatterListItem `json:"items"`
	HasMore    bool              `json:"has_more"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// ListMatters returns matters visible to callerUIDs in the space. The
// SourceChannelID filter is honoured ONLY for callers that have proven channel
// membership via octoim (user path with non-empty callerToken). For the bot
// path (callerToken==""), the channel filter is stripped so a leaked bot
// token cannot enumerate matters via channel-link.
// Caller still sees matters via creator / assignee / participant filters.
func (s *MatterService) ListMatters(ctx context.Context, spaceID string, filter repository.MatterFilter, callerToken string) (*MatterListResult, error) {
	if filter.SourceChannelID != nil {
		if callerToken == "" {
			// Bot path: strip channel filter unconditionally.
			filter.SourceChannelID = nil
		} else {
			member, err := s.im.IsChannelMember(ctx, callerToken, *filter.SourceChannelID, filter.CallerUIDs)
			if err != nil {
				log.Printf("[ERROR] ListMatters: IM IsChannelMember failed channel=%s: %v", *filter.SourceChannelID, err)
				return nil, apperr.Upstream(i18n.KeyChannelMembership)
			}
			if !member {
				filter.SourceChannelID = nil
			}
		}
	}
	// ChannelID is a strict result filter. Apply the same IM gating so a
	// caller cannot enumerate matters in a channel they don't belong to.
	if filter.ChannelID != nil {
		if callerToken == "" {
			filter.ChannelID = nil
		} else {
			member, err := s.im.IsChannelMember(ctx, callerToken, *filter.ChannelID, filter.CallerUIDs)
			if err != nil {
				log.Printf("[ERROR] ListMatters: IM IsChannelMember failed channel=%s: %v", *filter.ChannelID, err)
				return nil, apperr.Upstream(i18n.KeyChannelMembership)
			}
			if !member {
				filter.ChannelID = nil
			}
		}
	}
	matters, hasMore, err := s.matterRepo.ListBySpace(ctx, spaceID, filter)
	if err != nil {
		return nil, err
	}
	var nextCursor string
	if hasMore && len(matters) > 0 {
		last := matters[len(matters)-1]
		nextCursor = repository.EncodeCursor(repository.Cursor{
			CreatedAt: last.CreatedAt,
			ID:        last.ID,
		})
	}
	items, err := s.attachAssignees(ctx, matters)
	if err != nil {
		return nil, err
	}
	return &MatterListResult{
		Items:      items,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}, nil
}

// attachAssignees turns the bare matter slice into list items with assignees
// hydrated. Uses one batch query (ListByMatterIDs) regardless of slice size
// to avoid N+1. Each item's Assignees is a non-nil slice — empty when the
// matter has no assignees — so the JSON response uses [] rather than null.
func (s *MatterService) attachAssignees(ctx context.Context, matters []*model.Matter) ([]*MatterListItem, error) {
	items := make([]*MatterListItem, 0, len(matters))
	if len(matters) == 0 {
		return items, nil
	}
	ids := make([]string, 0, len(matters))
	for _, m := range matters {
		ids = append(ids, m.ID)
	}
	rows, err := s.assigneeRepo.ListByMatterIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	grouped := make(map[string][]*model.MatterAssignee, len(matters))
	for _, a := range rows {
		grouped[a.MatterID] = append(grouped[a.MatterID], a)
	}
	for _, m := range matters {
		assignees := grouped[m.ID]
		if assignees == nil {
			assignees = []*model.MatterAssignee{}
		}
		items = append(items, &MatterListItem{Matter: m, Assignees: assignees})
	}
	return items, nil
}

// MatterDetail is the enriched response for a single matter.
type MatterDetail struct {
	*model.Matter
	Assignees    []*model.MatterAssignee `json:"assignees"`
	Participants []string                `json:"participants"`
	Channels     []*model.MatterChannel  `json:"channels"`
}

// GetMatterForNotification loads a matter by ID+space without access checks.
func (s *MatterService) GetMatterForNotification(ctx context.Context, id, spaceID string) (*model.Matter, error) {
	return s.matterRepo.GetByID(ctx, id, spaceID)
}

// GetMatter loads a matter any of callerUIDs is allowed to read. callerToken
// is the user's IM auth token (empty for bot or non-channel-scoped requests);
// when sourceChannelID is supplied it gates channel-link access via the IM
// membership check (see canAccessMatter).
func (s *MatterService) GetMatter(ctx context.Context, id, spaceID string, callerUIDs []string, sourceChannelID, callerToken string) (*MatterDetail, error) {
	matter, err := s.matterRepo.GetByID(ctx, id, spaceID)
	if err != nil {
		return nil, err
	}
	ok, accessErr := s.canAccessMatter(ctx, matter, callerUIDs, sourceChannelID, callerToken)
	if accessErr != nil {
		return nil, accessErr
	}
	if !ok {
		return nil, apperr.Forbidden(i18n.KeyMatterView)
	}
	assignees, err := s.assigneeRepo.ListByMatter(ctx, id)
	if err != nil {
		return nil, err
	}
	participants, err := s.participantRepo.ListUserIDs(ctx, id)
	if err != nil {
		return nil, err
	}
	channels, err := s.channelRepo.ListByMatter(ctx, id)
	if err != nil {
		return nil, err
	}
	return &MatterDetail{
		Matter:       matter,
		Assignees:    assignees,
		Participants: participants,
		Channels:     channels,
	}, nil
}

// CanAccessMatter satisfies MatterAccessChecker.
func (s *MatterService) CanAccessMatter(ctx context.Context, matter *model.Matter, callerUIDs []string, channelID, callerToken string) (bool, error) {
	return s.canAccessMatter(ctx, matter, callerUIDs, channelID, callerToken)
}

// RequireChannelMember verifies via octoim that one of callerUIDs is a
// current member of channelID. Used as a gate before writing matter_channels
// : without this, an assignee on matter M could
// link M to an arbitrary channel they don't belong to, exposing M to that
// channel's real members.
//
// Returns nil for empty channelID OR empty callerToken — caller MUST handle
// the bot-path policy separately. Bot policy at each link write site:
//   - matter creation (manual / extract): allow (bounded one-shot at create)
//   - timeline auto-link / manual LinkChannel: deny (no expansion of existing
//     matters via bot path; let a user link first)
//
// Returns apperr.Forbidden if not member, apperr.Upstream on IM failure (so
// the request fails closed rather than silently accepting an unverifiable
// link).
func (s *MatterService) RequireChannelMember(ctx context.Context, callerToken, channelID string, callerUIDs []string) error {
	if channelID == "" || callerToken == "" {
		return nil
	}
	member, err := s.im.IsChannelMember(ctx, callerToken, channelID, callerUIDs)
	if err != nil {
		log.Printf("[ERROR] RequireChannelMember: IM lookup failed channel=%s: %v", channelID, err)
		return apperr.Upstream(i18n.KeyChannelMembership)
	}
	if !member {
		return apperr.Forbidden(i18n.KeyNotChannelMember)
	}
	return nil
}

// canAccessMatter returns (true, nil) if access is granted, (false, nil) if
// denied, or (false, err) on infrastructure failure (DB or IM upstream).
//
// channel_id is honoured ONLY for callers that have proven channel
// membership via octoim (user path with non-empty callerToken). Otherwise
// — including the bot path (callerToken=="") — the channel-link claim is
// stripped before reaching HasAccess, so authz must succeed via creator /
// assignee / participant. Defense-in-depth: even a leaked bot token cannot
// pivot through a known/guessed channel id.
func (s *MatterService) canAccessMatter(ctx context.Context, matter *model.Matter, callerUIDs []string, channelID, callerToken string) (bool, error) {
	// Fast path: creator check is in-memory, no DB or IM call needed.
	if s.isCreator(matter, callerUIDs) {
		return true, nil
	}

	effectiveChannel := ""
	if channelID != "" && callerToken != "" {
		member, err := s.im.IsChannelMember(ctx, callerToken, channelID, callerUIDs)
		if err != nil {
			log.Printf("[ERROR] canAccessMatter: IM IsChannelMember failed matter=%s channel=%s: %v", matter.ID, channelID, err)
			return false, apperr.Upstream(i18n.KeyChannelMembership)
		}
		if member {
			effectiveChannel = channelID
		}
		// IM-denied: channel claim already empty — falls through to assignee/participant.
	}

	ok, err := s.matterRepo.HasAccess(ctx, matter.ID, callerUIDs, effectiveChannel)
	if err != nil {
		log.Printf("[ERROR] canAccessMatter: HasAccess DB error matter=%s: %v", matter.ID, err)
		return false, err
	}
	return ok, nil
}

func (s *MatterService) isCreator(matter *model.Matter, callerUIDs []string) bool {
	for _, uid := range callerUIDs {
		if matter.CreatorID == uid {
			return true
		}
	}
	return false
}

func (s *MatterService) isAssigneeAny(ctx context.Context, matterID string, callerUIDs []string) (bool, error) {
	return s.assigneeRepo.IsAssigneeAny(ctx, matterID, callerUIDs)
}

// UpdateMatter applies editable fields. Creator or any assignee (expanded via
// callerUIDs) may edit.
func (s *MatterService) UpdateMatter(ctx context.Context, id, spaceID string, callerUIDs []string, title *string, description *string, deadline, remindAt *string) (*model.Matter, error) {
	matter, err := s.matterRepo.GetByID(ctx, id, spaceID)
	if err != nil {
		return nil, err
	}
	isAssignee, aErr := s.isAssigneeAny(ctx, id, callerUIDs)
	if aErr != nil {
		return nil, aErr
	}
	if !s.isCreator(matter, callerUIDs) && !isAssignee {
		return nil, apperr.ErrForbidden
	}
	actorID := actorFromCaller(callerUIDs)
	var activities []func()
	if title != nil && *title != matter.Title {
		oldTitle, newTitle := matter.Title, *title
		activities = append(activities, func() {
			recordActivity(ctx, s.activity, id, actorID, "title_changed",
				map[string]interface{}{"from": oldTitle, "to": newTitle})
		})
		matter.Title = *title
	}
	if description != nil {
		oldDesc := stringPtrOrEmpty(matter.Description)
		newDesc := *description
		if oldDesc != newDesc {
			summary := newDesc
			if len(summary) > 120 {
				summary = summary[:120]
			}
			activities = append(activities, func() {
				recordActivity(ctx, s.activity, id, actorID, "description_changed",
					map[string]interface{}{"summary": summary})
			})
		}
		matter.Description = description
	}
	if deadline != nil {
		t, err := ParseOptionalRFC3339(*deadline)
		if err != nil {
			return nil, apperr.InvalidInput(i18n.KeyDeadlineFormat)
		}
		oldUnix := timePtrUnix(matter.Deadline)
		newUnix := timePtrUnix(t)
		if oldUnix != newUnix {
			activities = append(activities, func() {
				recordActivity(ctx, s.activity, id, actorID, "deadline_changed",
					map[string]interface{}{"from": oldUnix, "to": newUnix})
			})
		}
		matter.Deadline = t
	}
	if remindAt != nil {
		t, err := ParseOptionalRFC3339(*remindAt)
		if err != nil {
			return nil, apperr.InvalidInput(i18n.KeyRemindAtFormat)
		}
		matter.RemindAt = t
	}
	if err := s.matterRepo.Update(ctx, matter); err != nil {
		return nil, err
	}
	for _, rec := range activities {
		rec()
	}
	return matter, nil
}

func timePtrUnix(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func actorFromCaller(callerUIDs []string) string {
	if len(callerUIDs) > 0 {
		return callerUIDs[0]
	}
	return ""
}

// ParseOptionalRFC3339 returns (nil, nil) for the empty string (clear the
// field) and otherwise parses as RFC3339. Parsed timestamps are normalized to
// UTC so storage and comparison are timezone-agnostic.
func ParseOptionalRFC3339(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, err
	}
	utc := t.UTC()
	return &utc, nil
}

// SetStatus changes a matter's status. Archive transitions (either direction)
// require Creator; open↔done is allowed for Creator or any assignee.
func (s *MatterService) SetStatus(ctx context.Context, id, spaceID string, callerUIDs []string, target model.MatterStatus) (*MatterDetail, error) {
	if !model.IsValidStatus(target) {
		return nil, apperr.InvalidInput(i18n.KeyStatusInvalid)
	}

	// Pre-check: if target is archived, only creator can do it.
	// (Full check including current-status-is-archived runs inside tx with row lock.)
	if target == model.MatterStatusArchived {
		matter, err := s.matterRepo.GetByID(ctx, id, spaceID)
		if err != nil {
			return nil, err
		}
		if !s.isCreator(matter, callerUIDs) {
			return nil, apperr.Forbidden(i18n.KeyOnlyCreatorArchive)
		}
	}

	var prevStatus model.MatterStatus
	var statusChanged bool
	err := s.tx.Do(ctx, func(r *repository.TxRepos) error {
		matter, err := r.Matter.GetByIDForUpdate(ctx, id, spaceID)
		if err != nil {
			return err
		}

		isCreator := s.isCreator(matter, callerUIDs)
		isAssigneeAny, _ := r.Assignee.IsAssigneeAny(ctx, id, callerUIDs)

		involvesArchived := target == model.MatterStatusArchived ||
			matter.Status == model.MatterStatusArchived
		if involvesArchived {
			if !isCreator {
				return apperr.Forbidden(i18n.KeyOnlyCreatorArchive)
			}
		} else {
			if !isCreator && !isAssigneeAny {
				return apperr.Forbidden(i18n.KeyOnlyCreatorOrAssigneeStat)
			}
		}

		if matter.Status == target {
			return nil
		}

		if matter.Status == model.MatterStatusArchived && target == model.MatterStatusDone {
			return apperr.InvalidInput(i18n.KeyTransitionArchived)
		}

		if err := r.Matter.UpdateStatus(ctx, id, spaceID, string(target)); err != nil {
			return err
		}
		prevStatus = matter.Status
		statusChanged = true
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Best-effort activity recorded post-tx, matching the pattern used by
	// UpdateMatter / AddAssignee etc. If the INSERT fails the mutation still
	// stands — matches existing behavior and keeps this mutation consistent
	// with the rest of matter_svc.go.
	if statusChanged {
		recordActivity(ctx, s.activity, id, actorFromCaller(callerUIDs), "status_changed",
			map[string]interface{}{"from": string(prevStatus), "to": string(target)})
	}

	matter, err := s.matterRepo.GetByID(ctx, id, spaceID)
	if err != nil {
		return nil, err
	}
	assignees, err := s.assigneeRepo.ListByMatter(ctx, id)
	if err != nil {
		return nil, err
	}
	participants, err := s.participantRepo.ListUserIDs(ctx, id)
	if err != nil {
		return nil, err
	}
	channels, err := s.channelRepo.ListByMatter(ctx, id)
	if err != nil {
		return nil, err
	}
	return &MatterDetail{
		Matter:       matter,
		Assignees:    assignees,
		Participants: participants,
		Channels:     channels,
	}, nil

}

func (s *MatterService) SoftDelete(ctx context.Context, id, spaceID string, callerUIDs []string) error {
	matter, err := s.matterRepo.GetByID(ctx, id, spaceID)
	if err != nil {
		return err
	}
	if !s.isCreator(matter, callerUIDs) {
		return apperr.ErrForbidden
	}
	return s.matterRepo.SoftDelete(ctx, id, spaceID)
}

// ListAssigneeIDs returns the user IDs of all assignees for a matter.
func (s *MatterService) ListAssigneeIDs(ctx context.Context, matterID string) ([]string, error) {
	assignees, err := s.assigneeRepo.ListByMatter(ctx, matterID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(assignees))
	for _, a := range assignees {
		ids = append(ids, a.UserID)
	}
	return ids, nil
}

// ListParticipantIDs returns every participant uid for a matter.
func (s *MatterService) ListParticipantIDs(ctx context.Context, matterID string) ([]string, error) {
	return s.participantRepo.ListUserIDs(ctx, matterID)
}

func (s *MatterService) AddAssignee(ctx context.Context, matterID, spaceID string, callerUIDs []string, assigneeUserID string) error {
	matter, err := s.matterRepo.GetByID(ctx, matterID, spaceID)
	if err != nil {
		return err
	}
	isAssignee, aErr := s.isAssigneeAny(ctx, matterID, callerUIDs)
	if aErr != nil {
		return aErr
	}
	if !s.isCreator(matter, callerUIDs) && !isAssignee {
		return apperr.ErrForbidden
	}
	if err := s.assigneeRepo.Create(ctx, &model.MatterAssignee{
		MatterID: matterID,
		UserID:   assigneeUserID,
	}); err != nil {
		return err
	}
	recordActivity(ctx, s.activity, matterID, actorFromCaller(callerUIDs), "assignee_added",
		map[string]interface{}{"user_id": assigneeUserID})
	return nil
}

func (s *MatterService) RemoveAssignee(ctx context.Context, matterID, spaceID string, callerUIDs []string, assigneeUserID string) error {
	matter, err := s.matterRepo.GetByID(ctx, matterID, spaceID)
	if err != nil {
		return err
	}

	isSelfUnassign := false
	for _, uid := range callerUIDs {
		if uid == assigneeUserID {
			isSelfUnassign = true
			break
		}
	}
	if !isSelfUnassign && !s.isCreator(matter, callerUIDs) {
		return apperr.ErrForbidden
	}
	if err := s.assigneeRepo.Delete(ctx, matterID, assigneeUserID); err != nil {
		return err
	}
	recordActivity(ctx, s.activity, matterID, actorFromCaller(callerUIDs), "assignee_removed",
		map[string]interface{}{"user_id": assigneeUserID})
	return nil
}

// LinkChannel attaches a channel to a matter. Creator or any assignee may link.
func (s *MatterService) LinkChannel(ctx context.Context, matterID, spaceID string, callerUIDs []string, channelID string, channelType uint8, channelName *string) (*model.MatterChannel, error) {
	if len(callerUIDs) == 0 {
		return nil, apperr.InvalidInput(i18n.KeyCallerIdentityReq)
	}
	matter, err := s.matterRepo.GetByID(ctx, matterID, spaceID)
	if err != nil {
		return nil, err
	}
	isAssignee, aErr := s.isAssigneeAny(ctx, matterID, callerUIDs)
	if aErr != nil {
		return nil, aErr
	}
	if !s.isCreator(matter, callerUIDs) && !isAssignee {
		return nil, apperr.ErrForbidden
	}
	mc := &model.MatterChannel{
		MatterID:    matterID,
		ChannelID:   channelID,
		ChannelType: channelType,
		ChannelName: channelName,
		LinkedBy:    callerUIDs[0],
	}
	if err := s.channelRepo.Create(ctx, mc); err != nil {
		return nil, err
	}
	detail := map[string]interface{}{"channel_id": channelID}
	if channelName != nil {
		detail["channel_name"] = *channelName
	}
	recordActivity(ctx, s.activity, matterID, actorFromCaller(callerUIDs), "channel_linked", detail)
	return mc, nil
}

// UnlinkChannel removes a channel link. Only Creator may unlink.
func (s *MatterService) UnlinkChannel(ctx context.Context, matterID, spaceID string, callerUIDs []string, channelID string) error {
	matter, err := s.matterRepo.GetByID(ctx, matterID, spaceID)
	if err != nil {
		return err
	}
	if !s.isCreator(matter, callerUIDs) {
		return apperr.ErrForbidden
	}
	if err := s.channelRepo.Delete(ctx, matterID, channelID); err != nil {
		return err
	}
	recordActivity(ctx, s.activity, matterID, actorFromCaller(callerUIDs), "channel_unlinked",
		map[string]interface{}{"channel_id": channelID})
	return nil
}
