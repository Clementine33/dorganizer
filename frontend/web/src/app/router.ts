import { createRouter, createWebHistory } from 'vue-router'
import LibrariesPage from '@/pages/LibrariesPage.vue'
import FolderDetailPage from '@/pages/FolderDetailPage.vue'
import WorksetsPage from '@/pages/WorksetsPage.vue'
import WorksetWorkspacePage from '@/pages/WorksetWorkspacePage.vue'
import ConversionSettingsPage from '@/pages/ConversionSettingsPage.vue'
import MemberDetailPage from '@/pages/MemberDetailPage.vue'
import MemberEditPage from '@/pages/MemberEditPage.vue'
import BatchEditPage from '@/pages/BatchEditPage.vue'

/**
 * Route table (design §5.1). Desktop and mobile use the same routes: layout
 * adapts to the container, never by navigating (R01). Search, filters and the
 * reviewed revision live in the query string as `q`, `filter`, `revision`;
 * transient state (checked names, frozen batch lists, unapplied edits) never
 * enters the URL (R03, R04).
 *
 * /worksets renders the list without entering the first entry (R02). Historical
 * results are read-only and have no editor route: `?revision=` selects the
 * reviewed revision, and the editor routes never carry it (E05).
 */
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
    { path: '/worksets', name: 'worksets', component: WorksetsPage },
    // Entering a workset and opening its conversion operation are the same
    // page: the overview is a section of the workspace, so the two URLs render
    // one workspace instead of jumping between pages.
    {
      path: '/worksets/:worksetId',
      name: 'workset-overview',
      component: WorksetWorkspacePage,
    },
    {
      path: '/worksets/:worksetId/conversion',
      name: 'conversion',
      component: WorksetWorkspacePage,
      // Every entry below is a detail/edit carrier of the same operation, so it
      // is a child of the workspace: the list and the shell survive the jump,
      // the carrier is chosen by the container tier (§7.3), and the edit is
      // never a page of its own that rebuilds the workbench (R01, R06).
      // `carrier` marks the route as occupying a carrier; `title` names the
      // carrier (a modal sheet and the narrow drill-down have no page heading
      // of their own).
      children: [
        {
          path: 'members/:memberId',
          name: 'conversion-member',
          component: MemberDetailPage,
          meta: { carrier: true, title: '文件夹详情' },
        },
        {
          path: 'members/:memberId/edit',
          name: 'conversion-member-edit',
          component: MemberEditPage,
          meta: { carrier: true, title: '修改此文件夹' },
        },
        {
          path: 'settings',
          name: 'conversion-settings',
          component: ConversionSettingsPage,
          meta: { carrier: true, title: '转换全局设置' },
        },
        {
          path: 'batch-edit',
          name: 'conversion-batch-edit',
          component: BatchEditPage,
          meta: { carrier: true, title: '批量修改' },
        },
      ],
    },
  ],
})
