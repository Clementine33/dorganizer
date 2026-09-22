package conversion

import (
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/workset"
)

// ClassifierTag is one user-added classifier tag in the global library. Tag is
// what the user wrote; NormalizedTag is its case-insensitive identity, and it is
// what makes adding the same tag twice a no-op rather than a duplicate.
type ClassifierTag struct {
	ID            int64
	Tag           string
	NormalizedTag string
	CreatedAt     time.Time
}

// ClassifierTagStore is the storage the custom tag library lives in.
type ClassifierTagStore interface {
	Tags() ([]ClassifierTag, error)
	AddTag(tag string) (*ClassifierTag, error)
	DeleteTag(id int64) error
}

// DefaultTags returns the maintained literal tags the configuration carries:
// the read-only half of the library. There is no compiled-in fallback, so an
// unreadable configuration offers none — but never a nil list, because "no
// defaults" and "the defaults could not be read" look the same to a browser.
func (c *Catalog) DefaultTags() []string {
	tags := c.settings.PruneLiteralTags()
	if tags == nil {
		return []string{}
	}
	return tags
}

// Tags returns the custom tags, ordered as the library keeps them.
func (c *Catalog) Tags() ([]ClassifierTag, error) {
	return c.tags.Tags()
}

// AddTag adds one custom tag to the library. An existing tag is returned as it
// was stored, not duplicated: the tag the user typed the second time is the
// same classifier as the first.
func (c *Catalog) AddTag(tag string) (*ClassifierTag, error) {
	trimmed := strings.TrimSpace(tag)
	if trimmed == "" {
		return nil, workset.NewError(
			workset.ErrKindInvalidArgument,
			"INVALID_TAG",
			"tag cannot be empty",
			nil,
		)
	}
	return c.tags.AddTag(trimmed)
}

// DeleteTag removes one custom tag by id. Whether the id names no row or the
// write itself failed is not distinguished here: the tag the caller asked to
// remove is not in the library either way, and that is what the route answers.
func (c *Catalog) DeleteTag(id int64) error {
	return c.tags.DeleteTag(id)
}
