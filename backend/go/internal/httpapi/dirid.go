package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/onsei/organizer/backend/internal/pathnorm"
)

// dirIDVersion is the fixed version marker of the directory identity. It is
// part of the hashed input, so a later change of the derivation yields new
// identities instead of silently reinterpreting old links.
const dirIDVersion = "onsei.dirid.v1"

// dirIDLen is the length of one directory id: 128 bits as lowercase hex.
const dirIDLen = 32

// dirID derives the navigation identity of one member directory from durable
// facts only: the version marker, the library, the canonical identity of the
// library root, and the exact relative path the inventory stores (spec §9 N3′,
// ADR 0008 §2).
//
// The stored path is hashed as it is: no case folding, no Unicode
// normalization, no repeated URL decoding. A directory is therefore identified
// by the name it really has, and the same root plus path always yields the same
// identity — before and after a rescan or a restart.
func dirID(libraryID, rootPath, relPath string) string {
	payload, err := json.Marshal([4]string{
		dirIDVersion,
		libraryID,
		pathnorm.RootPathKey(rootPath),
		relPath,
	})
	if err != nil {
		// A fixed string array cannot fail to encode; the empty value is the
		// one value that names no directory.
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:dirIDLen/2])
}

// validDirID reports whether a value is one directory id: exactly 32 lowercase
// hex characters. A malformed value is refused before any lookup, so a wrong
// shape is a bad request rather than an unknown directory.
func validDirID(value string) bool {
	if len(value) != dirIDLen {
		return false
	}
	for i := range len(value) {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// matchDirID finds the one library-relative directory whose identity is id. It
// reports the match, and whether more than one directory claims the identity:
// an ambiguous identity is never resolved to the first match (spec §9 I1′).
func matchDirID(candidates []string, libraryID, rootPath, id string) (rel string, found, ambiguous bool) {
	for _, candidate := range candidates {
		if dirID(libraryID, rootPath, candidate) != id {
			continue
		}
		if found {
			return "", false, true
		}
		rel, found = candidate, true
	}
	return rel, found, false
}
