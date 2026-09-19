// Central query-key factories. Pages and composables must never construct
// keys ad hoc: cache isolation, prefix invalidation, and stale-response
// protection all depend on these exact tuple shapes.
export const queryKeys = {
  libraries: {
    all: () => ['libraries'] as const,
    list: () => ['libraries', 'list'] as const,
    // The overview listing: every direct child directory of the root. Its
    // identity is the library-relative path, and the key carries the root
    // identity so a genuine root change invalidates it.
    dirsPrefix: (libraryId: string) => ['libraries', 'dirs', libraryId] as const,
    dirs: (libraryId: string, rootIdentity: string) =>
      ['libraries', 'dirs', libraryId, rootIdentity] as const,
    // One member tree, addressed by the member's library-relative path.
    memberTreesPrefix: (libraryId: string) => ['libraries', 'member-trees', libraryId] as const,
    memberTree: (libraryId: string, rootIdentity: string, relPath: string) =>
      ['libraries', 'member-trees', libraryId, rootIdentity, relPath] as const,
  },
  plans: {
    lists: () => ['plans', 'list'] as const,
    libraryPrefix: (libraryId: string) => ['plans', 'list', libraryId] as const,
    list: (libraryId: string, limit = 100) => ['plans', 'list', libraryId, limit] as const,
    detail: (planId: string) => ['plans', 'detail', planId] as const,
  },
  policySlots: {
    list: () => ['policy-slots', 'list'] as const,
  },
  classifierTags: {
    list: () => ['classifier-tags', 'list'] as const,
  },
  // Every mutable workset resource is keyed by (workset, operation): the same
  // workset id must never address two operations' drafts, sessions or
  // revisions through one cache entry.
  worksets: {
    all: () => ['worksets'] as const,
    listPrefix: () => ['worksets', 'list'] as const,
    list: (libraryId: string | null) => ['worksets', 'list', libraryId ?? ''] as const,
    // A library's current record for one operation: at most one, addressed by
    // the pair rather than by a record id the UI would have to know first.
    current: (libraryId: string, operation: string) =>
      ['worksets', 'current', libraryId, operation] as const,
    detail: (worksetId: string) => ['worksets', 'detail', worksetId] as const,
    operationPrefix: (worksetId: string) => ['worksets', 'operation', worksetId] as const,
    operation: (worksetId: string, operation: string) =>
      ['worksets', 'operation', worksetId, operation] as const,
    draft: (worksetId: string, operation: string) =>
      ['worksets', 'draft', worksetId, operation] as const,
    revisionsPrefix: (worksetId: string, operation: string) =>
      ['worksets', 'revisions', worksetId, operation] as const,
    revision: (worksetId: string, operation: string, planId: string) =>
      ['worksets', 'revisions', worksetId, operation, planId] as const,
    executionsPrefix: (worksetId: string, operation: string) =>
      ['worksets', 'executions', worksetId, operation] as const,
    execution: (worksetId: string, operation: string, executionId: string) =>
      ['worksets', 'executions', worksetId, operation, executionId] as const,
  },
} as const