package chat

import (
	"context"
	"time"
)

// MiniProgramEntry is a unified second-floor / discover catalog entry. It is the
// runtime snapshot the SOK IM backend serves to the App; the authoritative source
// is the SOK ops platform (out of scope here). The IM backend only reads and
// enforces it (status / grayscale / version / permission).
type MiniProgramEntry struct {
	ID            string    `bson:"id"`
	EntryType     string    `bson:"entryType"` // finclip | dapp
	Name          string    `bson:"name"`
	Description   string    `bson:"description"`
	IconURL       string    `bson:"iconUrl"`
	CategoryIDs   []string  `bson:"categoryIds"`
	FinclipAppID  string    `bson:"finclipAppId"`
	FinclipPath   string    `bson:"finclipPath"`
	FinclipQuery  string    `bson:"finclipQuery"`
	FinclipSeq    int32     `bson:"finclipSequence"`
	DappURL       string    `bson:"dappUrl"`
	DappTitle     string    `bson:"dappTitle"`
	Permissions   []string  `bson:"permissions"`
	Status        string    `bson:"status"` // online | offline | suspended
	MinAppVersion string    `bson:"minAppVersion"`
	Platforms     []string  `bson:"platforms"` // empty means all; else ios/android
	Scopes        []string  `bson:"scopes"`    // ticket scopes this entry may grant
	Revision      int64     `bson:"revision"`
	Sort          int32     `bson:"sort"`
	UpdatedAt     time.Time `bson:"updatedAt"`
}

func (MiniProgramEntry) TableName() string {
	return "mini_program_entry"
}

// MiniProgramCategory is a display category used to group catalog entries.
type MiniProgramCategory struct {
	ID   string `bson:"id"`
	Name string `bson:"name"`
	Sort int32  `bson:"sort"`
}

func (MiniProgramCategory) TableName() string {
	return "mini_program_category"
}

// MiniProgramFavorite persists a user's favorite entry set. Set semantics with a
// monotonically increasing revision guard against lost updates across devices.
type MiniProgramFavorite struct {
	UserID    string    `bson:"userId"`
	EntryIDs  []string  `bson:"entryIds"`
	Revision  int64     `bson:"revision"`
	UpdatedAt time.Time `bson:"updatedAt"`
}

func (MiniProgramFavorite) TableName() string {
	return "mini_program_favorite"
}

// MiniProgramRecentEntry is one recent-open record (one doc per user+entry).
type MiniProgramRecentEntry struct {
	UserID       string    `bson:"userId"`
	EntryID      string    `bson:"entryId"`
	LastOpenedAt time.Time `bson:"lastOpenedAt"`
}

func (MiniProgramRecentEntry) TableName() string {
	return "mini_program_recent"
}

type MiniProgramEntryInterface interface {
	Take(ctx context.Context, id string) (*MiniProgramEntry, error)
	FindByIDs(ctx context.Context, ids []string) ([]*MiniProgramEntry, error)
	// FindOnline returns online entries matching optional categoryId/keyword,
	// sorted by (sort, id) ascending. maxRevision is the current catalog revision.
	FindOnline(ctx context.Context, categoryID, keyword string) ([]*MiniProgramEntry, int64, error)
}

type MiniProgramCategoryInterface interface {
	FindAll(ctx context.Context) ([]*MiniProgramCategory, error)
}

type MiniProgramFavoriteInterface interface {
	Take(ctx context.Context, userID string) (*MiniProgramFavorite, error)
	Upsert(ctx context.Context, record *MiniProgramFavorite) error
}

type MiniProgramRecentInterface interface {
	// FindByUser returns recent entries for a user ordered by lastOpenedAt desc.
	FindByUser(ctx context.Context, userID string, limit int64) ([]*MiniProgramRecentEntry, error)
	Upsert(ctx context.Context, record *MiniProgramRecentEntry) error
	Count(ctx context.Context, userID string) (int64, error)
	// TrimOldest keeps at most keep records for the user, deleting the oldest.
	TrimOldest(ctx context.Context, userID string, keep int64) error
}
