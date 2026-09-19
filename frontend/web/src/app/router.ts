import { createRouter, createWebHistory } from 'vue-router'
import WorksetsPage from '@/pages/WorksetsPage.vue'
import OverviewPage from '@/pages/OverviewPage.vue'
import ConversionPage from '@/pages/ConversionPage.vue'
import ConversionSettingsPage from '@/pages/ConversionSettingsPage.vue'
import MemberDetailPage from '@/pages/MemberDetailPage.vue'
import MemberEditPage from '@/pages/MemberEditPage.vue'
import BatchEditPage from '@/pages/BatchEditPage.vue'
import ExecutionDetailPage from '@/pages/ExecutionDetailPage.vue'
import NotFoundPage from '@/pages/NotFoundPage.vue'
import MemberFiles from '@/features/folders/MemberFiles.vue'
import { installAddressHygiene } from './route-params'

/**
 * Route table (spec §2 N1, §9 N1′). Desktop and mobile use the same routes: the
 * layout adapts to the container, never by navigating.
 *
 * Every address carries identities only — a library id, a member id, or a
 * directory id — never a folder or file name, and never a search term. Search
 * and the checked names live in the workbench's transient state instead; the
 * few parameters a page does support are declared in `route-params.ts`.
 *
 * Static pages and member pages are separate routes on purpose: a member is
 * addressed as `/conversion/:memberId`, a page of the workbench as
 * `/conversion/settings`, and vue-router resolves the static segment first. The
 * route names are what navigation uses — no path is assembled by hand.
 *
 * The workbench's own history is not a chain: each page names its fixed parent,
 * and the retired long addresses are gone rather than redirected, because
 * guessing what an old link meant is worse than saying it does not exist.
 */
export const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    // The product entry keeps the name 工作集 and selects a media library:
    // everything below happens inside one library's workbench.
    { path: '/', redirect: '/worksets' },
    { path: '/worksets', name: 'worksets', component: WorksetsPage },

    // The workbench of one library. The overview and a member's file page are
    // the same page in two views, so returning from a member keeps the list's
    // selection, filters and scroll position.
    {
      path: '/worksets/:libraryId',
      name: 'workbench-overview',
      component: OverviewPage,
      children: [
        {
          path: 'f/:dirId',
          name: 'overview-files',
          component: MemberFiles,
        },
      ],
    },

    {
      path: '/worksets/:libraryId/conversion',
      name: 'conversion',
      component: ConversionPage,
      // Every entry below is a detail/edit carrier of the same record, so it is
      // a child of the workspace: the list and the shell survive the jump, the
      // carrier is chosen by the container tier, and the edit is never a page
      // of its own that rebuilds the workbench.
      // `carrier` marks the route as occupying a carrier; `title` names the
      // carrier (a modal sheet and the narrow drill-down have no page heading
      // of their own).
      children: [
        // The static pages come first: `/conversion/settings` is a page of the
        // workbench and must never be read as a member named "settings".
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
        // A member's files: the shared file module, addressed by the member's
        // stable id and resolved to its directory by the record.
        {
          path: ':memberId/files',
          name: 'conversion-member-files',
          component: MemberFiles,
        },
        {
          path: ':memberId',
          name: 'conversion-member',
          component: MemberDetailPage,
          meta: { carrier: true, title: '文件夹详情' },
        },
        {
          path: ':memberId/edit',
          name: 'conversion-member-edit',
          component: MemberEditPage,
          meta: { carrier: true, title: '修改此文件夹' },
        },
      ],
    },

    // An address that names no page of this workbench. The retired long routes
    // land here: they are not redirected to a guessed target.
    { path: '/:pathMatch(.*)*', name: 'not-found', component: NotFoundPage },
  ],
})

installAddressHygiene(router)
