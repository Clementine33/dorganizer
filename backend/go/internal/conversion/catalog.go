package conversion

// Catalog is the global conversion-policy catalog: the three fixed policy slots
// and the classifier tag library. Both are edited from the settings pages rather
// than from a workset, and both hold facts only the conversion rules can judge —
// what a policy must contain, what a classifier tag means — so the validation
// lives here and storage is reached through the ports below it.
//
// The tag library is two halves: the maintained literals the configuration
// carries, which are read-only, and the custom tags users add, which live in
// storage. They are offered together because to a classifier they are one list.
type Catalog struct {
	slots    PolicySlotStore
	tags     ClassifierTagStore
	settings Settings
}

// NewCatalog wires the catalog to its storage and configuration.
func NewCatalog(slots PolicySlotStore, tags ClassifierTagStore, settings Settings) *Catalog {
	return &Catalog{slots: slots, tags: tags, settings: settings}
}
