// Central query-key factories. Pages and composables must never construct
// keys ad hoc: cache isolation, prefix invalidation, and stale-response
// protection all depend on these exact tuple shapes.
export const queryKeys = {
  libraries: {
    list: () => ['libraries', 'list'] as const,
    foldersPrefix: (libraryId: string) => ['libraries', 'folders', libraryId] as const,
    folders: (libraryId: string, rootIdentity: string) =>
      ['libraries', 'folders', libraryId, rootIdentity] as const,
    treesPrefix: (libraryId: string) => ['libraries', 'folder-trees', libraryId] as const,
    tree: (libraryId: string, rootIdentity: string, folderId: string) =>
      ['libraries', 'folder-trees', libraryId, rootIdentity, folderId] as const,
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
    detail: (worksetId: string) => ['worksets', 'detail', worksetId] as const,
    operationPrefix: (worksetId: string) => ['worksets', 'operation', worksetId] as const,
    operation: (worksetId: string, operation: string) =>
      ['worksets', 'operation', worksetId, operation] as const,
    draft: (worksetId: string, operation: string) =>
      ['worksets', 'draft', worksetId, operation] as const,
    revisionsPrefix: (worksetId: string, operation: string) =>
      ['worksets', 'revisions', worksetId, operation] as const,
    revisionList: (worksetId: string, operation: string) =>
      ['worksets', 'revisions', worksetId, operation, 'list'] as const,
    revision: (worksetId: string, operation: string, planId: string) =>
      ['worksets', 'revisions', worksetId, operation, planId] as const,
  },
} as const