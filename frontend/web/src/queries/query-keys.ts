// Central query-key factories. Pages and composables must never construct
// keys ad hoc: cache isolation, prefix invalidation, and stale-response
// protection all depend on these exact tuple shapes.
export const queryKeys = {
  libraries: {
    all: ['libraries'] as const,
    list: () => ['libraries', 'list'] as const,
    foldersPrefix: (libraryId: string) => ['libraries', 'folders', libraryId] as const,
    folders: (libraryId: string, rootIdentity: string) =>
      ['libraries', 'folders', libraryId, rootIdentity] as const,
    treesPrefix: (libraryId: string) => ['libraries', 'folder-trees', libraryId] as const,
    tree: (libraryId: string, rootIdentity: string, folderId: string) =>
      ['libraries', 'folder-trees', libraryId, rootIdentity, folderId] as const,
  },
  plans: {
    all: ['plans'] as const,
    lists: () => ['plans', 'list'] as const,
    libraryPrefix: (libraryId: string) => ['plans', 'list', libraryId] as const,
    list: (libraryId: string, limit = 100) => ['plans', 'list', libraryId, limit] as const,
    details: () => ['plans', 'detail'] as const,
    detail: (planId: string) => ['plans', 'detail', planId] as const,
  },
} as const