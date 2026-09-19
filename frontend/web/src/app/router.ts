import { createRouter, createWebHistory } from 'vue-router'
import WorksetsPage from '@/pages/WorksetsPage.vue'
import OverviewPage from '@/pages/OverviewPage.vue'
import ConversionPage from '@/pages/ConversionPage.vue'
import ConversionSettingsPage from '@/pages/ConversionSettingsPage.vue'
import MemberDetailPage from '@/pages/MemberDetailPage.vue'
import MemberEditPage from '@/pages/MemberEditPage.vue'
import BatchEditPage from '@/pages/BatchEditPage.vue'
import ExecutionDetailPage from '@/pages/ExecutionDetailPage.vue'
import MemberFiles from '@/features/folders/MemberFiles.vue'

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
    // The product entry keeps the name 工作集 and selects a media library:
    // everything below happens inside one library's workbench (spec N1, N2).
    { path: '/', redirect: '/worksets' },
    { path: '/worksets', name: 'worksets', component: WorksetsPage },

    // The workbench of one library. The overview and a member's file page are
    // the same page in two views, so returning from a member keeps the list's
    // selection, filters and scroll position (N2).
    {
      path: '/worksets/libraries/:libraryId',
      name: 'workbench-overview',
      component: OverviewPage,
      children: [
        {
          path: 'files',
          name: 'overview-files',
          component: MemberFiles,
        },
      ],
    },

    {
      path: '/worksets/libraries/:libraryId/conversion',
      name: 'conversion',
      component: ConversionPage,
      // Every entry below is a detail/edit carrier of the same record, so it is
      // a child of the workspace: the list and the shell survive the jump, the
      // carrier is chosen by the container tier (§7.3), and the edit is never
      // a page of its own that rebuilds the workbench (R01, R06).
      // `carrier` marks the route as occupying a carrier; `title` names the
      // carrier (a modal sheet and the narrow drill-down have no page heading
      // of their own).
      children: [
        {
          // A member's files: the shared file module, addressed by the member's
          // stable id and resolved to its directory by the record.
          path: 'members/:memberId/files',
          name: 'conversion-member-files',
          component: MemberFiles,
        },
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
          path: 'execution',
          name: 'conversion-execution',
          component: ExecutionDetailPage,
          meta: { carrier: true, title: '执行结果' },
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
