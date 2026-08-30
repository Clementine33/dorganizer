import { createRouter, createWebHistory } from 'vue-router'
import LibrariesPage from '@/pages/LibrariesPage.vue'
import FolderDetailPage from '@/pages/FolderDetailPage.vue'
import WorksetsPage from '@/pages/WorksetsPage.vue'

export const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    { path: '/', redirect: '/libraries' },
    { path: '/libraries', name: 'libraries', component: LibrariesPage },
    {
      path: '/libraries/:libraryId/folders/:folderId',
      name: 'folder-detail',
      component: FolderDetailPage,
    },
    {
      path: '/worksets',
      name: 'worksets',
      component: WorksetsPage,
    },
    {
      path: '/worksets/:worksetId',
      name: 'workset-detail',
      component: WorksetsPage,
    },
  ],
})
