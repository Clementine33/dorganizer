import { createRouter, createWebHistory } from 'vue-router'
import LibrariesPage from '@/pages/LibrariesPage.vue'
import FolderDetailPage from '@/pages/FolderDetailPage.vue'
import PlansPage from '@/pages/PlansPage.vue'
import PlanReviewPage from '@/pages/PlanReviewPage.vue'

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
      path: '/plans',
      name: 'plans',
      component: PlansPage,
    },
    {
      path: '/plans/:id',
      name: 'plan-review',
      component: PlanReviewPage,
    },
  ],
})
