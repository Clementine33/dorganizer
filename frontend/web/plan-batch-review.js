  'use strict'

  // ────────────────────────────────────────────────────────────────────────────
  // Workset Feed 原型 —— FUTURE-CONTRACT · 仅产品设计，不代表现有后端接口
  //
  // 参考 plan-workset-feed.md（优先）与 plan-workbench-design-guidance.md。
  // 层级：Workset(工作集) → Folder(专辑批次) → Component(组件) → Lane(轨道)
  //       → FileDecision → Operation → Execute Phase。
  // 组件/Lane/Projected Inventory 字段沿用 reconcile 真实词汇
  //   Lanes: KEEP | KEEP_ALL | REBUILD_ALL | REBUILD | BLOCKED
  //   原因码: QUALITY_UNKNOWN / TARGET_PATH_CONFLICT / SOURCE_AMBIGUOUS / …
  //   锁: workflow execute 未接入 → 执行按钮永久 disabled。
  // 诚实性：当前 Plan list/detail 不返回 Workset identity、无 Workset API、
  //   无 Revision history API —— 这些字段在本文件中由 FUTURE-CONTRACT 标记，
  //   页面以低干扰说明注明边界；生成按钮为示意，不模拟真实成功。
  // ────────────────────────────────────────────────────────────────────────────

  const LIB = { library_id: 'lib_onsei', name: 'Onsei', root_path: '/mnt/media/onsei' }

  const stems = (prefix, n) => Array.from({ length: n }, (_, i) => `${prefix}_${String(i).padStart(2, '0')}`)

  // variant builders — keep / encode / delete decision rows with reason codes
  const variantsEncodeRebuild = (batch, n) => stems(batch, n).map((st) => ({
    stem: st,
    files: [
      { path: `${batch}/${st}.wav`, resolution: 'keep', reason: 'KEEP_LOSSLESS_TARGET' },
      { path: `${batch}/legacy/${st}_192.mp3`, resolution: 'delete', reason: 'OBSOLETE_ENCODED' },
      { path: `${batch}/${st}.mp3`, resolution: 'encode', reason: 'MATERIALIZE_ENCODED', target: `${batch}/${st}.mp3` },
    ],
  }))
  const variantsKeepAll = (batch, n) => stems(batch, n).map((st) => ({
    stem: st,
    files: [
      { path: `${batch}/${st}.wav`, resolution: 'keep', reason: 'KEEP_LOSSLESS_TARGET' },
      { path: `${batch}/${st}.mp3`, resolution: 'keep', reason: 'KEEP_ENCODED_SATISFIED' },
    ],
  }))
  const variantsLosslessRebuild = (batch, n) => stems(batch, n).map((st) => ({
    stem: st,
    files: [
      { path: `${batch}/${st}.flac`, resolution: 'encode', reason: 'MATERIALIZE_LOSSLESS', target: `${batch}/${st}.wav` },
      { path: `${batch}/${st}.mp3`, resolution: 'keep', reason: 'KEEP_ENCODED_SATISFIED' },
    ],
  }))
  const variantsBlocked = (batch, n) => stems(batch, n).map((st) => ({
    stem: st,
    files: [
      { path: `${batch}/${st}.wav`, resolution: 'keep', reason: 'KEEP_LOSSLESS_TARGET' },
      { path: `${batch}/${st}.mp3`, resolution: 'keep', reason: 'KEEP_ENCODED_SATISFIED' },
    ],
  }))

  const L = {
    keepAll:         [{ side: 'lossless', codec: 'wav', decision: 'KEEP' }, { side: 'encoded', codec: 'mp3', quality: '320 kbps', decision: 'KEEP_ALL' }],
    encodedRebuild:  [{ side: 'lossless', codec: 'wav', decision: 'KEEP' }, { side: 'encoded', codec: 'mp3', quality: '320 kbps', decision: 'REBUILD_ALL' }],
    losslessRebuild: [{ side: 'lossless', codec: 'wav', decision: 'REBUILD' }, { side: 'encoded', codec: 'mp3', quality: '320 kbps', decision: 'KEEP_ALL' }],
    blocked:         [{ side: 'lossless', codec: 'wav', decision: 'BLOCKED' }, { side: 'encoded', codec: 'mp3', quality: '320 kbps', decision: 'BLOCKED' }],
  }
  const RECIPE = { enc: { lanes: L.encodedRebuild, build: variantsEncodeRebuild }, keep: { lanes: L.keepAll, build: variantsKeepAll }, loss: { lanes: L.losslessRebuild, build: variantsLosslessRebuild } }

  // comp spec: { name, n, v: enc|keep|loss, block } — built per folder partition
  let seedI = 0
  function mkComps(specs) {
    return specs.map((s) => {
      const id = 'c' + String(++seedI).padStart(4, '0') + '9f2a' + String((seedI * 7919) % 0xffff, 16).padStart(4, '0') + 'b8e1d407c3a5f692'
      if (s.block) return { id, name: s.name, partition: s.partition, status: 'blocked', reason_code: s.block.code, message: s.block.msg, lanes: L.blocked, variants: variantsBlocked(s.batch, s.n) }
      const r = RECIPE[s.v]
      return { id, name: s.name, partition: s.partition, status: 'ok', lanes: r.lanes, variants: r.build(s.batch, s.n) }
    })
  }
  function deriveComponents(comps) {
    for (const c of comps) {
      let gen = 0, rem = 0, kep = 0
      for (const v of c.variants) for (const f of v.files) {
        if (f.resolution === 'encode') gen++
        else if (f.resolution === 'delete') rem++
        else kep++
      }
      c.inv = { gen, rem, kep }
    }
    return comps
  }
  function compsStats(comps) {
    const actionable = comps.filter((c) => c.status === 'ok')
    const blocked = comps.filter((c) => c.status === 'blocked')
    return {
      total: comps.length, actionable: actionable.length, blocked: blocked.length,
      gen: comps.reduce((n, c) => n + c.inv.gen, 0),
      rem: comps.reduce((n, c) => n + c.inv.rem, 0),
      ops: actionable.reduce((n, c) => n + c.inv.gen + c.inv.rem, 0),
    }
  }

  // helper: build a folder batch from partition spec.
  // parts.matched / parts.unmatched → arrays of comp specs (mkComps families)
  function mkFolder(wsIdx, fIdx, display, relPath, parts, stateStr) {
    const comps = []
    const partitions = ['matched', 'unmatched']
    partitions.forEach((p) => {
      const spec = (parts[p] || []).map((cs) => ({ ...cs, batch: display, partition: p }))
      comps.push(...deriveComponents(mkComps(spec)))
    })
    const stats = compsStats(comps)
    const blocked = stats.blocked
    const changed = stats.gen + stats.rem > 0
    const conclusion = stateStr === 'pending' ? 'PENDING' : blocked ? (stats.actionable > 0 ? 'PARTIAL' : 'BLOCKED') : changed ? 'ACTIONABLE' : 'NO_MATCH'
    return {
      folder_id: 'folder_' + display.toLowerCase().replace(/[^a-z0-9]+/g, '_').slice(0, 14),
      display,
      folder_path: LIB.root_path + '/' + display,
      state: stateStr, // planned | pending
      conclusion,
      stats,
      parts: {
        matched: { comps: comps.filter((c) => c.partition === 'matched') },
        unmatched: { comps: comps.filter((c) => c.partition === 'unmatched') },
      },
      fileCount: stats.gen + stats.rem + comps.reduce((n, c) => n + c.inv.kep, 0),
    }
  }

  const BLOCK = {
    unknown: { code: 'QUALITY_UNKNOWN', msg: '一个或多个编码文件无法确定质量，Planner 无法保证组件内编码输出一致。' },
    conflict: { code: 'TARGET_PATH_CONFLICT', msg: '目标路径被另一个组件条目占用；请调整编码目标或文件位置后重新规划。' },
    ambiguous: { code: 'SOURCE_AMBIGUOUS', msg: '同一 stem 存在多个等效的无损来源，无法确定为编码目标重建的源文件。' },
    unfulfillable: { code: 'LOSSLESS_TARGET_UNFULFILLABLE', msg: '该组无法满足无损 WAV 输出目标：缺少可用的无损来源。' },
  }

  // ── worksets（FUTURE-CONTRACT 数据形状，见 plan-workset-feed.md §15）─────
  const mkComp = (name, n, v, block) => ({ name, n, v, block })

  // folder builders
  const F = {
    rj015: () => mkFolder(0, 0, 'RJ01567288_夜間透過モデル', '', {
      // 分区 mutually exclusive：matched 一个组件（合并同一分区下所有文件）
      matched: [mkComp('track 00–11', 12, 'enc')],
      unmatched: [mkComp('SE 使用集', 3, 'loss')],
    }, 'planned'),
    rj010: () => mkFolder(0, 1, 'RJ01044712_アーカイブ Vol.2', '', {
      matched: [mkComp('Vol.2 軌跡', 5, 'enc')],
    }, 'planned'),
    rj012: () => mkFolder(0, 2, 'RJ01277330_ボイスドラマ第2章', '', {
      matched: [mkComp('本编 6 軌', 6, 'keep', BLOCK.conflict)],
    }, 'planned'),
    bgm: () => mkFolder(0, 3, 'BGM_ost_extract', '', {
      unmatched: [mkComp('op・ed', 3, 'loss')],
    }, 'planned'),
    se01: () => mkFolder(0, 4, '音效_SE_pack_01', '', {
      unmatched: [mkComp('常用SE', 4, 'keep', BLOCK.unknown)],
    }, 'planned'),
    se02: () => mkFolder(0, 5, '音效_SE_pack_02', '', {
      unmatched: [mkComp('本编SE', 3, 'keep')],
    }, 'planned'),
    w2023: () => mkFolder(0, 6, 'WorkArchive_2023', '', {
      matched: [mkComp('旧录音修正', 3, 'loss')],
    }, 'planned'),
    w2022: () => mkFolder(0, 7, 'WorkArchive_2022', '', {
      matched: [mkComp('旧录音保留', 2, 'keep')],
    }, 'planned'),
    drama1: () => mkFolder(0, 8, 'Dramas_第一章', '', {
      matched: [mkComp('精讲', 3, 'keep', BLOCK.ambiguous)],
    }, 'planned'),
    drama2: () => mkFolder(0, 9, 'Dramas_第二章', '', {
      matched: [mkComp('精讲', 3, 'enc')],
    }, 'planned'),
    drama3: () => mkFolder(0, 10, 'Dramas_第三章', '', {
      unmatched: [mkComp('预告', 2, 'keep')],
    }, 'planned'),
  }

  // fully-blocked album batches (for the BLOCKED workset — every folder blocked)
  const Fb = {
    rj012b: () => mkFolder(0, 2, 'RJ01277330_ボイスドラマ第2章', '', {
      matched: [mkComp('本编 6 軌', 6, 'keep', BLOCK.conflict)],
    }, 'planned'),
    bgmB: () => mkFolder(0, 3, 'BGM_ost_extract', '', {
      unmatched: [mkComp('op・ed', 3, 'loss', BLOCK.unfulfillable)],
    }, 'planned'),
    drama1b: () => mkFolder(0, 8, 'Dramas_第一章', '', {
      matched: [mkComp('精讲', 3, 'keep', BLOCK.ambiguous)],
    }, 'planned'),
  }

  // pending folder: no components, honest "awaiting first revision" state.
  // seed 是生成计划版本时产出快照的构建器（FUTURE-CONTRACT：仅模拟）。
  const pendingFolder = (i, display, seedFn) => {
    const f = mkFolder(-1, i, display, '', {}, 'pending')
    f.seed = seedFn
    return f
  }

  const worksets = [
    {
      workset_id: 'ws_a1f2c3d4',
      title: '夏季整理',
      created_at: '2026-08-29 09:40',
      updated_at: '2026-08-29 10:32',
      folders: [F.rj015(), F.rj010(), F.rj012(), F.bgm(), F.se01(), F.se02(), F.w2023()],
      current_revision: {
        plan_id: 'plan_8f3a2c', snapshot_token: 'snapshot-20260829103200.000000',
        created_at: '2026-08-29 10:32', plan_kind: 'workflow', status: 'ready',
        summary_reason: 'PARTIAL',
      },
      revisions: [
        { plan_id: 'plan_8f3a2c', at: '2026-08-29 10:32', lifecycle: 'ready' },
        { plan_id: 'plan_71d102', at: '2026-08-28 19:45', lifecycle: 'stale' },
        { plan_id: 'plan_5ca881', at: '2026-08-27 21:00', lifecycle: 'executed' },
      ],
    },
    {
      workset_id: 'ws_b2e3d4e5',
      title: '秋季整理',
      created_at: '2026-08-26 08:15',
      updated_at: '2026-08-26 08:15',
      folders: [F.bgm(), F.se02(), F.w2022(), F.drama3()],
      current_revision: {
        plan_id: 'plan_7e1b9d04', snapshot_token: 'snapshot-20260826081500.000000',
        created_at: '2026-08-26 08:15', plan_kind: 'workflow', status: 'ready',
        summary_reason: 'ACTIONABLE',
      },
      revisions: [{ plan_id: 'plan_7e1b9d04', at: '2026-08-26 08:15', lifecycle: 'ready' }],
    },
    {
      workset_id: 'ws_c3f4e5f6',
      title: '冬眠归档',
      created_at: '2026-08-23 20:20',
      updated_at: '2026-08-23 20:20',
      folders: [Fb.rj012b(), F.se01(), Fb.drama1b(), Fb.bgmB()],
      current_revision: {
        plan_id: 'plan_5c04a162', snapshot_token: 'snapshot-20260823202000.000000',
        created_at: '2026-08-23 20:20', plan_kind: 'workflow', status: 'ready',
        summary_reason: 'BLOCKED',
      },
      revisions: [{ plan_id: 'plan_5c04a162', at: '2026-08-23 20:20', lifecycle: 'ready' }],
    },
    {
      workset_id: 'ws_d4a5f6a7',
      title: '新番前瞻',
      created_at: '2026-08-21 11:05',
      updated_at: '2026-08-21 11:06',
      folders: [F.drama3(), F.w2022(), F.se02()],
      current_revision: {
        plan_id: 'plan_3f92e7a8', snapshot_token: 'snapshot-20260821110600.000000',
        created_at: '2026-08-21 11:06', plan_kind: 'workflow', status: 'ready',
        summary_reason: 'NO_MATCH',
      },
      revisions: [{ plan_id: 'plan_3f92e7a8', at: '2026-08-21 11:06', lifecycle: 'ready' }],
    },
    {
      workset_id: 'ws_e5b6a7b8',
      title: '旧盘修复',
      created_at: '2026-08-13 14:00',
      updated_at: '2026-08-29 09:00',
      folders: [F.rj010(), F.drama2()],
      current_revision: {
        plan_id: 'plan_0bf2d4e6', snapshot_token: 'snapshot-20260813090000.000000',
        created_at: '2026-08-13 09:00', plan_kind: 'workflow', status: 'stale',
        summary_reason: 'PARTIAL',
      },
      revisions: [{ plan_id: 'plan_0bf2d4e6', at: '2026-08-13 09:00', lifecycle: 'stale' }],
    },
    {
      workset_id: 'ws_f6c7b8c9',
      title: '会议录音',
      created_at: '2026-08-29 11:00',
      updated_at: '2026-08-29 11:00',
      folders: [pendingFolder(0, 'WorkArchive_2023', F.w2023), pendingFolder(1, 'WorkArchive_2022', F.w2022), pendingFolder(2, 'Dramas_第三章', F.drama3)],
      current_revision: null, // 待规划：没有修订
      revisions: [],
    },
  ]

  // workset-level summary (from folders)
  function wsSummary(ws) {
    const all = ws.folders
    const blockedAlbums = all.filter((f) => f.conclusion === 'BLOCKED').length + all.filter((f) => f.conclusion === 'PARTIAL').length
    const gen = all.reduce((n, f) => n + f.stats.gen, 0)
    const rem = all.reduce((n, f) => n + f.stats.rem, 0)
    const changedAlbums = all.filter((f) => f.stats.gen + f.stats.rem > 0).length
    return { albums: all.length, blockedAlbums, gen, rem, changedAlbums }
  }
  worksets.forEach((ws) => { ws.summary = wsSummary(ws) })
  worksets.forEach((ws, i) => (ws.worksetIdx = i))

  const WORKFLOW_POLICY = { source_kind: 'preset', name: 'balanced', version: 1 }
  const DESIRED = { lossless: { codec: 'WAV' }, encoded: { codec: 'MP3', quality: '320 kbps' } }
  const makeWorkflowDraft = () => ({
    schema_version: 1,
    policy: { kind: 'preset', name: 'balanced', version: 1 },
    classifier: { name: 'effect-direction', version: 1 },
    matched: { lossless: 'wav', encoded: 'mp3', bitrate: 320 },
    unmatched: { lossless: 'flac', encoded: 'aac', bitrate: 256 },
    future_slots: ['rename', 'organize_directories', 'metadata_sidecars'],
  })
  worksets.forEach((ws) => { ws.workflow_draft = makeWorkflowDraft() })

  // ── display helpers ────────────────────────────────────────────────
  const PART_LABEL = { matched: '无音效 matched', unmatched: '有音效 unmatched' }
  const RES_LABEL = { keep: '保留', encode: '生成', delete: '移除' }
  const RES_ICON = { keep: '✓', encode: '+', delete: '−' }
  const REASON_LABEL = {
    KEEP_LOSSLESS_TARGET: '无损目标已满足',
    KEEP_ENCODED_SATISFIED: '编码目标已满足',
    MATERIALIZE_ENCODED: '重建编码输出',
    MATERIALIZE_LOSSLESS: '生成无损输出',
    OBSOLETE_ENCODED: '旧编码已失效',
    OBSOLETE_LOSSLESS: '旧无损已失效',
  }
  const CONCLUSION_PILL = {
    PARTIAL: 'partial', BLOCKED: 'blocked', ACTIONABLE: 'actionable', NO_MATCH: 'nomatch', STALE: 'stale', PENDING: 'pending',
  }
  const REASON_TEXT = {
    PARTIAL: 'PARTIAL · 部分组件可执行',
    BLOCKED: 'BLOCKED · 当前步骤无法执行',
    ACTIONABLE: 'ACTIONABLE · 全部组件可执行',
    NO_MATCH: 'NO_MATCH · 无需调整',
  }
  const LANE_TONE = (d) => (d === 'BLOCKED' ? 'blocked' : d === 'REBUILD_ALL' ? 'rebuildall' : d === 'REBUILD' ? 'rebuild' : d === 'KEEP_ALL' ? 'keepall' : 'keep')

  function el(tag, cls, text) {
    const node = document.createElement(tag)
    if (cls) node.className = cls
    if (text !== undefined) node.textContent = text
    return node
  }
  function svg(paths, cls, strokeW) {
    const wrap = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
    wrap.setAttribute('viewBox', '0 0 24 24')
    wrap.setAttribute('fill', 'none')
    wrap.setAttribute('stroke', 'currentColor')
    wrap.setAttribute('stroke-width', String(strokeW || 1.8))
    wrap.setAttribute('stroke-linecap', 'round')
    wrap.setAttribute('stroke-linejoin', 'round')
    if (cls) wrap.setAttribute('class', cls)
    paths.forEach((d) => {
      const p = document.createElementNS('http://www.w3.org/2000/svg', 'path')
      p.setAttribute('d', d)
      wrap.appendChild(p)
    })
    return wrap
  }
  const ICONS = {
    lock: ['M5 11h14v9H5z', 'M8 11V8a4 4 0 0 1 8 0v3'],
    warn: ['M10.3 3.9L1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z', 'M12 9v4', 'M12 17h.01'],
    search: ['M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16z', 'M21 21l-4.35-4.35'],
    refresh: ['M3 12a9 9 0 0 1 15-6.7L21 8', 'M21 3v5h-5', 'M21 12a9 9 0 0 1-15 6.7L3 16', 'M21 21v-5h5'],
    playFuture: ['M5 4l10 8-10 8z', 'M19 5v14'],
    folder: ['M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z'],
    copy: ['M9 9h9v9H9z', 'M6 15V6h9'],
    chevR: ['M9 6l6 6-6 6'],
    plus: ['M12 5v14', 'M5 12h14'],
  }

  const app = document.getElementById('app')
  const state = {
    wsIdx: 0,
    workbenchStage: 'review', // configure | review; execute/result remain locked
    composeStep: 0,           // compose 流程列表当前步骤：0=任务配置, 1=流程摘要（最后一环）
    filter: 'all',       // all | change | blocked | pending
    query: '',
    selBatch: null,      // folder id | null
    selComp: null,       // comp id | null → batch-level inspector when null
    openBatches: new Set(),
    revIdx: 0,           // 0 = current revision; >0 = history (FUTURE-CONTRACT read-back)
    libTarget: null,
    flash: null,         // transient (原型模拟) notice after generating a revision
    generating: null,    // { ws: workset_id } while simulating a planning pass
  }

  // default open: first blocked/partial batch, else first changed, else first
  ;(() => {
    const ws = worksets[0]
    const fb = ws.folders.find((f) => f.conclusion === 'BLOCKED' || f.conclusion === 'PARTIAL')
      || ws.folders.find((f) => f.stats.gen + f.stats.rem > 0)
      || ws.folders[0]
    if (fb) state.openBatches.add(fb.folder_id)
    state.selBatch = fb && fb.folder_id
    // default selection: first component of first partition of that batch
    const c0 = fb && (fb.parts.matched.comps[0] || fb.parts.unmatched.comps[0])
    state.selComp = c0 ? c0.id : null
  })()

  const curWs = () => worksets[state.wsIdx]
  const curRev = (ws) => ws.current_revision // FUTURE-CONTRACT: revision switching is product-design only

  // ── 从流程配置生成计划版本（FUTURE-CONTRACT · 前端模拟）────────────────
  // 配置流程是产出 snapshot 的唯一入口：先解析待规划专辑，再以当前 workflow
  // draft 生成一个新的不可变 Revision。真实后端应为
  // POST /api/v1/worksets/:id/revisions（当前未定义）。
  const REV_SEQ = { n: 0 }
  function nextPlanId() {
    REV_SEQ.n++
    return 'plan_' + (0x8f + REV_SEQ.n).toString(16) + Math.random().toString(16).slice(2, 6)
  }
  function generateRevision(ws) {
    const hadRevision = Boolean(ws.current_revision)

    ws.folders.forEach((f, i) => {
      if (f.state !== 'pending') return
      const generated = f.seed ? f.seed() : mkFolder(i, i, f.display, '', {}, 'planned')
      // carry over identity + folder id so the batch box stays addressable
      generated.folder_id = f.folder_id
      ws.folders[i] = generated
    })
    ws.summary = wsSummary(ws)

    // workset state transitions: draft → planned
    const now = '2026-08-29 11:0' + (1 + (REV_SEQ.n % 9))
    const planId = nextPlanId()
    const rev = {
      plan_id: planId,
      snapshot_token: 'snapshot-' + now.replace(/[^\d]/g, ''),
      created_at: now,
      plan_kind: 'workflow',
      status: 'ready',
      summary_reason: ws.summary.blockedAlbums > 0
        ? (ws.summary.albums - ws.summary.blockedAlbums > 0 ? 'PARTIAL' : 'BLOCKED')
        : (ws.summary.gen + ws.summary.rem > 0 ? 'ACTIONABLE' : 'NO_MATCH'),
      workflow_snapshot: JSON.parse(JSON.stringify(ws.workflow_draft)),
      // revision-list row fields (same shape as the demo revisions):
      at: now,
      lifecycle: 'ready',
    }
    ws.current_revision = rev
    ws.revisions = [rev, ...ws.revisions]
    ws.updated_at = now

    // re-select a first batch + first component like initial load
    const fb = ws.folders.find((f) => f.conclusion === 'BLOCKED' || f.conclusion === 'PARTIAL')
      || ws.folders.find((f) => f.stats.gen + f.stats.rem > 0)
      || ws.folders[0]
    state.openBatches = new Set(fb && fb.folder_id ? [fb.folder_id] : [])
    state.selBatch = fb && fb.folder_id
    const c0 = fb && (fb.parts.matched.comps[0] || fb.parts.unmatched.comps[0])
    state.selComp = c0 ? c0.id : null
    state.filter = 'all'
    state.revIdx = 0
    state.workbenchStage = 'review'

    state.flash = {
      kind: hadRevision ? 'regenerated' : 'generated',
      planId,
      count: ws.folders.length,
      ts: Date.now(),
    }
    setTimeout(() => { if (state.flash) { state.flash = null; rerender() } }, 4200)
    return true
  }
  // 单批入口 → 整集生成(FUTURE-CONTRACT:Revision 是工作集级快照)
  function batchGenerate(ws) {
    if (state.generating) return
    state.generating = { ws: ws.workset_id }
    rerender()
    setTimeout(() => { state.generating = null; if (generateRevision(ws)) rerender() }, 900)
  }
  const curComp = (ws) => {
    if (!state.selComp) return null
    for (const f of ws.folders) for (const p of ['matched', 'unmatched']) {
      const c = f.parts[p].comps.find((c) => c.id === state.selComp)
      if (c) return { comp: c, folder: f, partition: p }
    }
    return null
  }
  const curBatch = (ws) => ws.folders.find((f) => f.folder_id === state.selBatch) || null

  // ── global header ──────────────────────────────────────────────────
  function renderGlobalHeader() {
    const head = el('header', 'g-header')
    head.dataset.odId = 'global-header'
    const brand = el('div', 'g-brand')
    const mark = el('span', 'g-mark')
    mark.appendChild(svg(['M4 14v-4M9 18V6M14 15V9M19 17V7'], null, 2.2))
    brand.append(mark, el('span', 'name', '音声整理'), el('span', 'sep', '/'), el('span', 'sec', '工作集'))
    head.appendChild(brand)
    head.appendChild(el('span', 'g-lib', '当前媒体库：' + LIB.root_path))
    head.appendChild(el('span', 'g-note', '原型 · Workset 契约待后端接入'))
    return head
  }

  // ── A. workset feed ────────────────────────────────────────────────
  function feedMark(ws) {
    const rev = ws.current_revision
    if (state.revIdx > 0) return null // history read-back shows current pill on header only
    if (!rev) return el('span', 'fpill pending', '待规划')
    if (rev.status === 'stale') return el('span', 'fpill stale', 'STALE')
    return el('span', 'fpill ' + CONCLUSION_PILL[rev.summary_reason], rev.summary_reason)
  }
  function renderFeed() {
    const feed = el('aside', 'd-feed')
    feed.dataset.odId = 'workset-feed'
    feed.setAttribute('aria-label', '工作集列表')

    const head = el('div', 'feed-head')
    const t = el('div', 'fh-t')
    t.append(el('span', 't', '工作集'), el('span', 'sub', worksets.length + ' 条 · 最近更新'))
    head.appendChild(t)
    const fh = el('div', 'fh-new')
    fh.appendChild(svg(ICONS.plus, null, 1.8))
    fh.appendChild(el('span', null, '新建工作集'))
    fh.title = '从媒体库选择专辑文件夹创建（FUTURE-CONTRACT · 示意）'
    fh.addEventListener('click', () => { state.wsIdx = -1; show('lib') })
    head.appendChild(fh)
    feed.appendChild(head)

    worksets.forEach((ws, i) => {
      const item = el('button', 'feed-item' + (i === state.wsIdx ? ' active' : ''))
      item.type = 'button'
      item.dataset.ws = ws.workset_id
      item.setAttribute('aria-current', i === state.wsIdx ? 'true' : 'false')

      const top = el('span', 'fitem-top')
      top.append(el('span', 'fitem-title', ws.title))
      const fpill = feedMark(ws)
      if (fpill) top.appendChild(fpill)
      item.appendChild(top)
      // 信息收敛：ID+专辑数+阻止 归一行，版本号/生成/移除进入详情层
      item.appendChild(el('span', 'fitem-id', ws.workset_id + ' · ' + ws.summary.albums + ' 个专辑' + (ws.summary.blockedAlbums ? ' · ' + ws.summary.blockedAlbums + ' 阻止' : '')))

      const sub = el('span', 'fitem-sub')
      sub.appendChild(el('span', null, '更新 ' + ws.updated_at.slice(5, 16)))
      item.appendChild(sub)

      item.addEventListener('click', () => selectWs(i))
      feed.appendChild(item)
    })

    // feed keyboard: Up/Down/Home/End/Enter
    feed.addEventListener('keydown', (e) => {
      const items = [...feed.querySelectorAll('.feed-item')]
      const idx = items.indexOf(document.activeElement)
      if (idx === -1) return
      if (e.key === 'ArrowDown') { e.preventDefault(); items[(idx + 1) % items.length].focus() }
      else if (e.key === 'ArrowUp') { e.preventDefault(); items[(idx - 1 + items.length) % items.length].focus() }
      else if (e.key === 'Home') { e.preventDefault(); items[0].focus() }
      else if (e.key === 'End') { e.preventDefault(); items[items.length - 1].focus() }
      else if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); items[idx].click() }
    })
    return feed
  }

  function selectWs(i) {
    if (i < 0) return
    state.wsIdx = i
    state.filter = 'all'
    state.query = ''
    state.revIdx = 0
    state.workbenchStage = worksets[i].current_revision ? 'review' : 'configure'
    state.selBatch = null
    const ws = worksets[i]
    const fb = ws.folders.find((f) => f.conclusion === 'BLOCKED' || f.conclusion === 'PARTIAL')
      || ws.folders.find((f) => f.stats.gen + f.stats.rem > 0)
      || ws.folders[0]
    state.openBatches = new Set(fb ? [fb.folder_id] : [])
    state.selBatch = fb && fb.folder_id
    const c0 = fb && (fb.parts.matched.comps[0] || fb.parts.unmatched.comps[0])
    state.selComp = c0 ? c0.id : null
    rerender()
  }

  // ── active workset header ──────────────────────────────────────────
  function renderWsHeader(ws) {
    const head = el('header', 'ws-header')
    head.dataset.odId = 'ws-header'
    const crumb = el('div', 'crumb')
    const t = el('div', 'crumb-title')
    t.append(el('span', 't', ws.title), el('span', 'sep', '/'), el('span', 'id', ws.workset_id))
    crumb.appendChild(t)
    const meta = el('div', 'crumb-meta')
    const rev = ws.current_revision
    meta.append(
      el('span', null, ws.summary.albums + ' 个专辑文件夹'),
      el('span', 'sep', '·'),
      el('span', null, rev ? '当前版本 ' + rev.plan_id : '尚未生成计划版本 · 不显示假 ID'), // 无 Revision 不伪造
      el('span', 'sep', '·'),
      el('span', null, '生成于 ' + (rev ? rev.created_at : ws.created_at)),
    )
    crumb.appendChild(meta)
    head.appendChild(crumb)

    const right = el('div', 'header-right')
    if (rev) {
      right.appendChild(el('span', 'pill kind', 'Workflow'))
      const reason = el('span', 'pill reason-' + CONCLUSION_PILL[rev.summary_reason], REASON_TEXT[rev.summary_reason])
      reason.appendChild(el('span', 'dot'))
      right.appendChild(reason)
      right.appendChild(el('span', 'pill life', rev.status === 'stale' ? 'stale' : rev.status))
    } else {
      right.appendChild(el('span', 'pill pending', '待规划 · 尚无计划版本'))
    }
    const back = el('button', 'btn btn-ghost')
    back.type = 'button'
    back.append(el('span', null, '返回媒体库'))
    back.addEventListener('click', () => show('lib'))
    right.appendChild(back)
    head.appendChild(right)
    return head
  }

  // ── stage navigation (belongs to the workset) ──────────────────────
  function renderStage(ws) {
    const bar = el('div', 'b-stage')
    bar.dataset.odId = 'stage-nav'
    const rev = ws.current_revision
    const stages = [
      { n: '1', key: 'configure', label: '配置流程', cls: state.workbenchStage === 'configure' ? 'current' : 'done', hint: '设置 Workflow 任务、顺序与每个任务的策略' },
      { n: '2', key: 'review', label: '审阅计划', cls: !rev ? 'locked' : state.workbenchStage === 'review' ? 'current' : 'done', hint: rev ? '审阅当前不可变计划版本' : '先完成流程配置并生成计划版本' },
      { n: '3', key: 'execute', label: '执行', cls: 'locked', hint: '当前版本暂不支持执行 Workflow 计划' },
      { n: '4', key: 'result', label: '结果', cls: 'locked', hint: '执行接入后可用' },
    ]
    stages.forEach((s, i) => {
      if (i > 0) bar.appendChild(el('span', 'stage-sep', '→'))
      const node = el('button', 'stage ' + s.cls)
      node.type = 'button'
      node.title = s.hint
      const inner = el('span', 'stage-label')
      inner.append(el('span', 'n', s.n), el('span', null, s.label))
      node.appendChild(inner)
      if (s.cls === 'locked') {
        node.appendChild(svg(ICONS.lock, null, 1.8))
        node.setAttribute('aria-disabled', 'true')
      } else if (s.key === 'configure' || s.key === 'review') {
        node.addEventListener('click', () => { state.workbenchStage = s.key; rerender() })
      }
      bar.appendChild(node)
    })
    return bar
  }

  // ── configure workflow (draft → immutable plan revision) ─────────────
  function configSelect(value, options, onChange, aria) {
    const field = el('label', 'config-field')
    const select = document.createElement('select')
    select.setAttribute('aria-label', aria)
    options.forEach(([v, label]) => {
      const o = document.createElement('option')
      o.value = v
      o.textContent = label
      o.selected = String(v) === String(value)
      select.appendChild(o)
    })
    select.addEventListener('change', (e) => onChange(e.target.value))
    field.appendChild(select)
    return field
  }

  function renderComposeFlow(ws) {
    const rail = el('aside', 'compose-flow')
    rail.append(el('div', 'compose-eyebrow', 'Workflow draft'), el('div', 'compose-title', '任务流程'))
    rail.appendChild(el('div', 'compose-desc', '这里配置下一版计划要执行的线性任务。文件夹集合属于工作集，不是流程步骤；流程摘要是最后一环。'))
    const list = el('div', 'flow-list')
    const steps = [
      { n: '01', name: '音频转换与输出协调', tech: 'reconcile_audio_outputs', state: '已启用 · 当前可配置', active: true },
      { n: '02', name: '文件重命名', tech: 'rename · 待定任务', state: '预留位置 · 尚未定义' },
      { n: '03', name: '目录整理', tech: 'organize_directories · 待定任务', state: '预留位置 · 尚未定义' },
      { n: '04', name: '元数据与 Sidecar', tech: 'metadata_sidecars · 待定任务', state: '预留位置 · 尚未定义' },
      { n: '05', name: '流程摘要', tech: 'workflow draft summary', state: '全部任务的配置摘要', active: false },
    ]
    steps.forEach((s, i) => {
      const isActive = i === state.composeStep
      const node = el('button', 'flow-step ' + (isActive ? 'active' : (i === 4 ? 'future' : 'future')))
      node.type = 'button'
      node.appendChild(el('span', 'flow-num', s.n))
      const body = el('div')
      body.append(el('div', 'flow-name', s.name), el('div', 'flow-tech', s.tech), el('div', 'flow-state', s.state))
      node.appendChild(body)
      node.addEventListener('click', () => { state.composeStep = i; rerender() })
      list.appendChild(node)
    })
    rail.appendChild(list)
    const note = el('div', 'compose-note')
    note.append(el('strong', null, '步骤位置 ≠ 执行阶段。'), document.createTextNode(' materialize、validate、commit、remove 是音频步骤内部 Phase，不在这里作为任务添加。'))
    rail.appendChild(note)
    return rail
  }

  function renderProfileSection(ws, key, title, tech) {
    const draft = ws.workflow_draft
    const profile = draft[key]
    const sec = el('section', 'config-section')
    const heading = el('div', 'config-section-title')
    heading.append(el('strong', null, title), el('code', null, tech))
    sec.appendChild(heading)

    const loss = el('div', 'config-grid')
    loss.appendChild(el('span', 'config-label', '无损输出目标'))
    loss.appendChild(configSelect(profile.lossless, [['wav', 'WAV'], ['flac', 'FLAC'], ['none', '不管理无损输出']], (v) => { profile.lossless = v }, title + '无损输出'))
    loss.appendChild(el('div', 'config-field'))
    loss.lastChild.append(el('span', null, 'Planner 将推导保留、重建与清理动作'))
    sec.appendChild(loss)

    const enc = el('div', 'config-grid')
    enc.appendChild(el('span', 'config-label', '编码输出目标'))
    enc.appendChild(configSelect(profile.encoded, [['mp3', 'MP3'], ['aac', 'AAC / M4A'], ['none', '不管理编码输出']], (v) => { profile.encoded = v }, title + '编码格式'))
    enc.appendChild(configSelect(profile.bitrate, [['320', '320 kbps'], ['256', '256 kbps'], ['192', '192 kbps']], (v) => { profile.bitrate = Number(v) }, title + '编码质量'))
    sec.appendChild(enc)
    return sec
  }

  function renderComposer(ws) {
    const shell = el('div', 'compose-shell')
    shell.dataset.odId = 'workflow-composer'
    shell.appendChild(renderComposeFlow(ws))

    const main = el('main', 'compose-main')
    if (state.composeStep === 1) {
      // 流程摘要作为配置流程最后一环：只保留两个 pane（左流程列表 + 右侧摘要/配置）
      main.append(el('div', 'compose-eyebrow', 'Last step · summary'), el('h2', 'compose-title', '流程摘要'))
      main.appendChild(el('p', 'compose-desc', '汇总本次流程配置。生成计划版本会使用此配置创建不可变 Revision。'))
      const summary = el('div', 'compose-summary')
      const rows = [
        ['Schema', 'workflow v' + ws.workflow_draft.schema_version, true],
        ['处理范围', ws.summary.albums + ' 个专辑批次'],
        ['已启用任务', '1'],
        ['待定任务位置', String(ws.workflow_draft.future_slots.length)],
        ['策略', ws.workflow_draft.policy.name + '@' + ws.workflow_draft.policy.version, true],
        ['无音效目标', ws.workflow_draft.matched.lossless.toUpperCase() + ' + ' + ws.workflow_draft.matched.encoded.toUpperCase() + ' ' + ws.workflow_draft.matched.bitrate],
        ['有音效目标', ws.workflow_draft.unmatched.lossless.toUpperCase() + ' + ' + ws.workflow_draft.unmatched.encoded.toUpperCase() + ' ' + ws.workflow_draft.unmatched.bitrate],
        ['当前 Revision', ws.current_revision ? ws.current_revision.plan_id : '尚无', true],
      ]
      rows.forEach(([k, v, mono]) => {
        const row = el('div', 'compose-summary-row')
        row.append(el('span', 'k', k), el('span', 'v' + (mono ? ' mono' : ''), v))
        summary.appendChild(row)
      })
      main.appendChild(summary)
      main.appendChild(el('div', 'compose-note', 'FUTURE-CONTRACT：Workset Workflow Draft 与 Revision 创建接口尚未接入；本页只演示正确的信息架构。'))
      shell.appendChild(main)
      return shell
    }

    main.append(el('div', 'compose-eyebrow', 'Step 01 · configurable'), el('h2', 'compose-title', '音频转换与输出协调'))
    main.appendChild(el('p', 'compose-desc', 'reconcile_audio_outputs 对每个分类分区声明期望的最终音频集合。Planner 根据现存库存推导 KEEP、REBUILD、编码和移除操作；这里不直接配置底层文件动作。'))

    const card = el('div', 'config-card')
    const ch = el('div', 'config-card-head')
    ch.append(el('h3', null, '步骤策略'), el('p', null, '策略与 classifier 会被快照进新 Plan Revision；修改不会改写已有版本。'))
    card.appendChild(ch)
    const source = el('section', 'config-section')
    const sh = el('div', 'config-section-title')
    sh.append(el('strong', null, '策略来源'), el('code', null, 'PolicySource'))
    source.appendChild(sh)
    const sg = el('div', 'config-grid')
    sg.append(el('span', 'config-label', '预设'))
    sg.appendChild(configSelect(ws.workflow_draft.policy.name, [['balanced', 'balanced@1'], ['archive', 'archive@1'], ['compact', 'compact@1']], (v) => { ws.workflow_draft.policy.name = v }, '策略预设'))
    const classifier = el('div', 'config-field')
    classifier.append(el('span', null, 'classifier'), el('span', null, ws.workflow_draft.classifier.name + '@' + ws.workflow_draft.classifier.version))
    sg.appendChild(classifier)
    source.appendChild(sg)
    card.appendChild(source)
    card.appendChild(renderProfileSection(ws, 'matched', '无音效', 'matched'))
    card.appendChild(renderProfileSection(ws, 'unmatched', '有音效', 'unmatched'))
    main.appendChild(card)

    const warning = el('div', 'compose-note')
    warning.append(el('strong', null, ws.current_revision ? '生成新版本：' : '首次规划：'), document.createTextNode(ws.current_revision ? ' 当前计划版本保持不可变；此配置将创建新的 Revision，并使下游步骤的 Projected Inventory 重新计算。' : ' 将对工作集内全部专辑批次运行该线性 Workflow，并创建第一个不可变 Revision。'))
    main.appendChild(warning)
    shell.appendChild(main)
    return shell
  }

  function renderComposeActions(ws) {
    const bar = el('footer', 'compose-actions')
    bar.appendChild(el('span', 'hint', '保存配置时创建新快照，不会修改已有 Plan Revision。'))
    const buttons = el('div', 'buttons')
    if (ws.current_revision) {
      const review = el('button', 'btn btn-ghost', '返回当前审阅')
      review.type = 'button'
      review.addEventListener('click', () => { state.workbenchStage = 'review'; rerender() })
      buttons.appendChild(review)
    }
    const generate = el('button', 'btn btn-primary')
    generate.type = 'button'
    generate.dataset.odId = 'generate-revision-from-workflow'
    generate.append(svg(ICONS.playFuture, null, 1.8), el('span', null, ws.current_revision ? '生成新计划版本' : '生成计划版本'))
    generate.title = 'FUTURE-CONTRACT · 使用当前流程配置生成不可变计划版本'
    if (state.generating && state.generating.ws === ws.workset_id) {
      generate.disabled = true
      generate.lastChild.textContent = '正在规划…'
    } else generate.addEventListener('click', () => batchGenerate(ws))
    buttons.appendChild(generate)
    bar.appendChild(buttons)
    return bar
  }

  function renderRibbon(ws) {
    const ribbon = el('div', 'c-ribbon')
    ribbon.dataset.odId = 'workflow-ribbon'
    ribbon.setAttribute('role', 'tablist')
    ribbon.setAttribute('aria-label', '工作流步骤')
    const rev = ws.current_revision

    if (!rev) {
      const none = el('div', 'slot-norev')
      const t = el('div')
      t.append(el('div', 'sn-t', '当前工作集尚无计划版本'), el('div', 'sn-s', ws.summary.albums + ' 个专辑文件夹等待规划'))
      none.appendChild(t)
      const hint = el('button', 'btn btn-primary')
      hint.type = 'button'
      hint.append(svg(ICONS.playFuture, null, 1.8), el('span', null, '配置 Workflow'))
      hint.title = '进入配置流程，设置音频转换任务与未来任务位置'
      hint.addEventListener('click', () => { state.workbenchStage = 'configure'; rerender() })
      none.appendChild(hint)
      ribbon.appendChild(none)
      return ribbon
    }

    const slot = el('div', 'slot')
    slot.tabIndex = 0
    slot.setAttribute('role', 'tab')
    slot.setAttribute('aria-selected', 'true')
    slot.appendChild(el('span', 'slot-order', '01'))
    const body = el('div')
    const name = el('div', 'slot-name')
    name.append(el('span', 'cn', '音频输出协调'), el('span', 'tech', 'reconcile_audio_outputs'))
    body.appendChild(name)
    const meta = el('div', 'slot-meta')
    const stepStatus = rev.summary_reason === 'BLOCKED' ? 'blocked' : rev.summary_reason === 'PARTIAL' ? 'partially_blocked' : 'ok'
    const tone = stepStatus === 'blocked' ? 's-blocked' : stepStatus === 'partially_blocked' ? 's-partial' : 's-ok'
    const label = stepStatus === 'blocked' ? 'BLOCKED' : stepStatus === 'partially_blocked' ? 'PARTIAL' : 'OK'
    const gen = ws.summary.gen + ws.summary.rem
    meta.append(
      el('span', tone, label + ' · ' + ws.summary.albums + ' 个专辑'),
      el('span', null, gen + ' 项操作'),
      el('span', null, '策略 ' + WORKFLOW_POLICY.name + '@' + WORKFLOW_POLICY.version),
    )
    body.appendChild(meta)
    slot.appendChild(body)
    ribbon.appendChild(slot)

    ribbon.appendChild(el('span', 'stage-sep', '→'))
    const future = el('div', 'slot-empty')
    future.append(svg(ICONS.playFuture, 'fw'), el('span', null, '未来步骤'), el('span', null, 'rename · organize · metadata · 尚未启用'))
    ribbon.appendChild(future)
    return ribbon
  }

  // ── batch pane ─────────────────────────────────────────────────────
  function visibleBatches(ws) {
    let list = ws.folders.slice()
    if (state.filter === 'change') list = list.filter((f) => f.stats.gen + f.stats.rem > 0)
    if (state.filter === 'blocked') list = list.filter((f) => f.conclusion === 'BLOCKED' || f.conclusion === 'PARTIAL')
    if (state.filter === 'pending') list = list.filter((f) => f.state === 'pending')
    if (state.query) {
      const q = state.query.toLowerCase()
      list = list.filter((f) => (f.display + ' ' + f.folder_path).toLowerCase().includes(q))
    }
    return list
  }

  function renderBatchToolbar(ws) {
    const bar = el('div', 'ws-toolbar')
    // 左侧 Feed 已含「N 个专辑 · N 阻止」简表，此处不再重复计数行。
    ;[['all', '全部'], ['change', '有变化'], ['blocked', '已阻止'], ['pending', '待规划']].forEach(([k, label]) => {
      const b = el('button', 'filter' + (state.filter === k ? ' on' : ''), label)
      b.type = 'button'
      b.addEventListener('click', () => { state.filter = k; rerender() })
      bar.appendChild(b)
    })

    const search = el('label', 'ws-search')
    search.appendChild(svg(ICONS.search))
    const input = document.createElement('input')
    input.type = 'search'
    input.placeholder = '搜索专辑或路径'
    input.value = state.query
    input.setAttribute('aria-label', '搜索专辑或路径')
    input.addEventListener('input', (e) => { state.query = e.target.value; rerender({ focusSearch: true }) })
    search.appendChild(input)
    bar.appendChild(search)
    return bar
  }

  function renderComponent(ws, c) {
    const box = el('section', 'comp' + (state.selComp === c.id ? ' selected' : ''))
    box.dataset.component = c.id
    const head = el('div', 'comp-head')
    head.setAttribute('role', 'button')
    head.tabIndex = 0
    head.dataset.head = c.id
    head.addEventListener('click', () => { state.selComp = c.id; state.selBatch = null; rerender() })
    head.addEventListener('keydown', (e) => {
      if (e.target !== head) return
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); state.selComp = c.id; rerender(); return }
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        const rows = [...document.querySelectorAll('.comp-head')]
        const idx = rows.indexOf(head)
        if (idx < 0) return
        e.preventDefault()
        const next = e.key === 'ArrowDown' ? (idx + 1) % rows.length : (idx - 1 + rows.length) % rows.length
        rows[next].focus()
      }
    })

    const lanes = el('div', 'lanes')
    c.lanes.forEach((lane) => {
      const tone = LANE_TONE(lane.decision)
      const cell = el('div', 'lane decision-' + tone)
      cell.appendChild(el('span', 'l-k', lane.side === 'lossless' ? '无损输出' : '编码输出'))
      const v = el('span', 'l-v')
      v.append(el('span', 'codec', lane.codec.toUpperCase()))
      if (lane.quality) v.appendChild(el('span', 'q', lane.quality))
      cell.appendChild(v)
      cell.appendChild(el('span', 'l-d', lane.decision))
      if (lane.decision === 'BLOCKED' && c.reason_code) cell.appendChild(el('span', 'l-msg', c.reason_code))
      lanes.appendChild(cell)
    })
    head.appendChild(lanes)

    const inv = el('div', 'inventory')
    inv.appendChild(el('span', 'inv-k', '计划后库存'))
    if (c.status === 'blocked') {
      inv.appendChild(el('div', 'inv-zero', '0 可执行操作 · 决策可审阅'))
    } else {
      const row = el('div', 'inv-row')
      if (c.inv.gen) { const g = el('span', 'gen', '+' + c.inv.gen); g.appendChild(el('span', 'lbl', ' 生成')); row.appendChild(g) }
      if (c.inv.rem) { const r = el('span', 'rem', '−' + c.inv.rem); r.appendChild(el('span', 'lbl', ' 移除')); row.appendChild(r) }
      if (c.inv.kep) { const k = el('span', 'kep', String(c.inv.kep)); k.appendChild(el('span', 'lbl', ' 保留')); row.appendChild(k) }
      if (!c.inv.gen && !c.inv.rem) row.append(el('span', 'kep', '无变化'))
      inv.appendChild(row)
    }
    head.appendChild(inv)

    box.appendChild(head)
    return box
  }

  function renderBatch(ws, f) {
    const box = el('section', 'bat' + (state.selBatch === f.folder_id ? ' selected' : ''))
    box.dataset.batch = f.folder_id
    const open = state.openBatches.has(f.folder_id)

    const head = el('div', 'bat-head')
    head.setAttribute('role', 'button')
    head.tabIndex = 0
    head.dataset.bhead = f.folder_id
    const toggle = (e) => {
      if (e.target.closest && e.target.closest('.jump-btn')) return
      state.selBatch = f.folder_id
      state.selComp = null
      if (state.openBatches.has(f.folder_id)) state.openBatches.delete(f.folder_id)
      else state.openBatches.add(f.folder_id)
      rerender()
    }
    head.addEventListener('click', toggle)
    head.addEventListener('keydown', (e) => {
      if (e.target !== head) return
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggle(e); return }
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        const heads = [...document.querySelectorAll('.bat-head')].filter((h) => h.offsetParent !== null)
        const idx = heads.indexOf(head)
        if (idx < 0) return
        e.preventDefault()
        const next = e.key === 'ArrowDown' ? (idx + 1) % heads.length : (idx - 1 + heads.length) % heads.length
        heads[next].focus()
      }
    })

    const title = el('div', 'bat-title')
    const nm = el('div', 'nm')
    nm.appendChild(el('span', 'n', f.display))
    title.appendChild(nm)
    const fp = el('div', 'fp', f.folder_path)
    fp.title = f.folder_path
    title.appendChild(fp)
    const meta = el('div', 'meta')
    meta.appendChild(el('span', null, f.fileCount + ' 个文件'))
    title.appendChild(meta)
    head.appendChild(title)

    const right = el('div', 'bat-right')
    if (f.state === 'pending') {
      right.appendChild(el('span', 'fpill pending', '待规划 · 请先配置流程'))
    } else {
      right.appendChild(el('span', 'fpill ' + CONCLUSION_PILL[f.conclusion], f.conclusion))
    }
    // 库存变化在展开后的组件行「计划后库存」展示，批头不再重复。
    const chev = el('span', 'bchev')
    chev.setAttribute('aria-hidden', 'true')
    chev.appendChild(svg(ICONS.chevR))
    right.appendChild(chev)
    const jumpB = el('button', 'jump-btn')
    jumpB.type = 'button'
    jumpB.setAttribute('aria-label', '在媒体库文件树中打开 ' + f.display)
    jumpB.title = '在媒体库文件树中打开'
    jumpB.appendChild(svg(ICONS.folder, null, 1.6))
    jumpB.addEventListener('click', (e) => { e.stopPropagation(); state.selBatch = f.folder_id; state.libTarget = { path: f.folder_path }; show('lib') })
    right.appendChild(jumpB)
    head.appendChild(right)

    box.appendChild(head)

    if (open) box.appendChild(renderBatchDetail(ws, f))
    return box
  }

  function renderBatchDetail(ws, f) {
    const detail = el('div', 'bat-detail')
    if (f.state === 'pending') {
      const note = el('div', 'bat-pending-note')
      note.append(
        svg(ICONS.playFuture, null, 1.6),
        el('span', null, '该专辑已加入工作集，但当前计划版本未覆盖此批次。生成新版本后这里会显示组件审阅内容。'),
      )
      detail.appendChild(note)
      return detail
    }
    const matched = f.parts.matched.comps
    const unmatched = f.parts.unmatched.comps
    const grid = el('div', 'part-grid')
    const build = (partition, comps) => {
      const sec = el('div', 'part-sec')
      const h = el('div', 'part-h')
      const pcn = el('span', 'pcn', PART_LABEL[partition].split(' ')[0])
      const pt = el('span', 'pt', PART_LABEL[partition].split(' ')[1])
      h.append(pcn, pt, el('span', 'n', comps.length + ' 个组件'))
      sec.appendChild(h)
      comps.forEach((c) => sec.appendChild(renderComponent(ws, c)))
      return sec
    }
    // 上下两个 Box：无音效 matched 在上，有音效 unmatched 在下
    if (matched.length) grid.appendChild(build('matched', matched))
    if (unmatched.length) grid.appendChild(build('unmatched', unmatched))
    detail.appendChild(grid)
    return detail
  }

  function renderBatchPane(ws) {
    const pane = el('div', 'd-batch')
    pane.dataset.odId = 'album-batch-pane'
    pane.appendChild(renderBatchToolbar(ws))
    const listEl = el('div', 'batch-list')
    const list = visibleBatches(ws)
    if (!list.length) {
      const emptyText = el('div', 'ws-empty')
      emptyText.append(el('div', 'bh', '没有匹配的专辑批次'), el('div', 'bp', '没有符合当前筛选的专辑批次，清除筛选或搜索后再试。'))
      listEl.appendChild(emptyText)
    }
    list.forEach((f) => listEl.appendChild(renderBatch(ws, f)))
    pane.appendChild(listEl)
    return pane
  }

  // ── inspector (two-level: batch / component) ────────────────────────
  function inspRow(k, v, mono) {
    const row = el('div', 'insp-row')
    row.append(el('span', 'k', k), el('span', 'v' + (mono ? ' mono' : ''), v))
    return row
  }
  function renderRevisionList(ws) {
    const sec = el('div', 'insp-sec')
    sec.appendChild(el('div', 'insp-h', '计划版本'))
    const rows = el('div', 'insp-rows')
    ws.revisions.forEach((r, i) => {
      const isCur = i === 0
      const btn = el('button', 'rev-row' + (state.revIdx === i ? ' cur' : ''))
      btn.type = 'button'
      btn.append(el('span', null, (isCur ? '● ' : '') + r.plan_id))
      btn.append(el('span', 'st', r.at.slice(5, 16) + (i === 0 ? ' · 当前' : ' · ' + r.lifecycle)))
      btn.addEventListener('click', () => { state.revIdx = i; rerender() })
      rows.appendChild(btn)
    })
    if (!ws.revisions.length) rows.appendChild(el('div', 'insp-row', '尚无计划版本'))
    sec.appendChild(rows)
    return sec
  }
  function renderInspector(ws) {
    const panel = el('aside', 'd-inspector')
    panel.dataset.odId = 'inspector'
    panel.setAttribute('aria-label', '检查器')

    // batch-level inspector
    const f = curBatch(ws)
    const sel = curComp(ws)

    if (sel) {
      // — component inspector —
      const c = sel.comp
      const sec1 = el('div', 'insp-sec')
      sec1.appendChild(el('div', 'insp-h', '组件'))
      const idRow = el('div', 'insp-id')
      const full = el('span', 'full', c.id)
      full.title = '组件 ID · 结构身份（非内容身份）'
      idRow.appendChild(full)
      const copy = el('button', 'copy-btn')
      copy.type = 'button'
      copy.setAttribute('aria-label', '复制组件 ID')
      copy.appendChild(svg(ICONS.copy, null, 1.7))
      copy.addEventListener('click', () => { if (navigator.clipboard) navigator.clipboard.writeText(c.id) })
      idRow.appendChild(copy)
      sec1.appendChild(idRow)
      const rows = el('div', 'insp-rows')
      rows.append(
        inspRow('组件', c.name),
        inspRow('所属专辑', sel.folder.display, true),
        inspRow('partition', PART_LABEL[c.partition] || c.partition, true),
      )
      sec1.appendChild(rows)
      panel.appendChild(sec1)

      if (c.status === 'blocked') {
        const bx = el('div', 'insp-blocked')
        const bt = el('div', 'bt')
        bt.append(svg(ICONS.warn), el('span', null, '组件已阻止'))
        bx.appendChild(bt)
        bx.appendChild(el('span', 'code', c.reason_code))
        bx.appendChild(el('div', 'msg', c.message))
        bx.appendChild(el('div', 'msg', '本组件不会产生可执行操作。其他组件仍可继续。'))
        panel.appendChild(bx)
      }

      const desire = el('div', 'insp-sec')
      desire.appendChild(el('div', 'insp-h', '输出期望 · ' + WORKFLOW_POLICY.name + '@' + WORKFLOW_POLICY.version))
      desire.appendChild(el('div', 'desire-ctx', '配置流程设置 · ' + (PART_LABEL[c.partition] || c.partition)))
      const dl = el('div', 'desire')
      c.lanes.forEach((lane) => {
        const isLossless = lane.side === 'lossless'
        const spec = isLossless ? DESIRED.lossless : DESIRED.encoded
        const tone = LANE_TONE(lane.decision)
        const row = el('div', 'desire-row' + (tone === 'rebuild' || tone === 'rebuildall' ? ' rebuild' : tone === 'blocked' ? ' blocked' : ''))
        row.append(el('span', 'd-t', isLossless ? '无损输出' : '编码输出'), el('span', 'd-s', spec.codec + (spec.quality ? ' · ' + spec.quality : '')))
        const tag = lane.decision === 'BLOCKED' ? '已阻止' : lane.decision === 'REBUILD' || lane.decision === 'REBUILD_ALL' ? '将重建' : '已满足'
        row.appendChild(el('span', 'd-tag', tag + ' ' + lane.decision))
        dl.appendChild(row)
      })
      desire.appendChild(dl)
      panel.appendChild(desire)

      const invSec = el('div', 'insp-sec')
      invSec.appendChild(el('div', 'insp-h', '库存'))
      const ir = el('div', 'insp-rows')
      ir.appendChild(inspRow('现存库存', c.inv.kep + ' 个文件'))
      if (c.status === 'blocked') ir.appendChild(inspRow('计划后库存', '排除于执行集合'))
      else ir.appendChild(inspRow('计划后库存', '+' + c.inv.gen + ' 生成 · −' + c.inv.rem + ' 移除 · ' + c.inv.kep + ' 保留'))
      invSec.appendChild(ir)
      panel.appendChild(invSec)

      const changeSec = el('div', 'insp-sec')
      changeSec.appendChild(el('div', 'insp-h', '文件变化明细'))
      const all = []
      c.variants.forEach((v) => v.files.forEach((fl) => all.push(fl)))
      all.sort((a, b) => a.path.localeCompare(b.path))
      const ch = el('div', 'insp-rows')
      all.forEach((fl) => {
        const row = el('div', 'insp-file')
        const top = el('div', 'if-top')
        top.append(el('span', 'if-chip ' + (fl.resolution === 'encode' ? 'gen' : fl.resolution === 'delete' ? 'rem' : ''), RES_ICON[fl.resolution] + ' ' + RES_LABEL[fl.resolution]))
        const fp = el('span', 'if-path' + (fl.target ? ' muted' : ''), fl.path)
        fp.title = fl.path
        top.appendChild(fp)
        if (fl.target) top.append(el('span', 'if-arrow', '→'), el('span', 'if-target', fl.target))
        row.appendChild(top)
        const rs = el('span', 'if-reason', REASON_LABEL[fl.reason] || fl.reason)
        rs.title = fl.reason
        row.appendChild(rs)
        ch.appendChild(row)
      })
      if (!all.length) ch.appendChild(el('span', null, '无文件变化'))
      changeSec.appendChild(ch)
      panel.appendChild(changeSec)
    } else if (f) {
      // — batch inspector —
      const sec1 = el('div', 'insp-sec')
      sec1.appendChild(el('div', 'insp-h', '专辑批次'))
      const rows = el('div', 'insp-rows')
      rows.append(
        inspRow('名称', f.display),
        inspRow('完整路径', f.folder_path, true),
        inspRow('所属工作集', ws.title),
        inspRow('计划版本', ws.current_revision ? ws.current_revision.plan_id : '待规划', true),
        inspRow('partition', (f.parts.matched.comps.length ? '无音效 matched ✓' : '') + (f.parts.matched.comps.length && f.parts.unmatched.comps.length ? ' · ' : '') + (f.parts.unmatched.comps.length ? '有音效 unmatched ✓' : '') || '—'),
        inspRow('组件 / 阻止', f.stats.total + ' / ' + f.stats.blocked),
        inspRow('库存变化', f.stats.gen + f.stats.rem > 0 ? '+' + f.stats.gen + ' 生成 · −' + f.stats.rem + ' 移除 · ' + f.stats.total + ' 保留' : '无变化'),
        inspRow('批次结论', f.conclusion, true),
      )
      sec1.appendChild(rows)
      panel.appendChild(sec1)

      if (f.state === 'pending') {
        const note = el('div', 'bat-pending-note')
        note.append(svg(ICONS.playFuture, null, 1.6), el('span', null, '修订覆盖状态：待规划 — 下一版计划生成时纳入此专辑。'))
        panel.appendChild(note)
      } else if (ws.current_revision && ws.current_revision.status === 'stale') {
        const note = el('div', 'bat-pending-note')
        note.append(svg(ICONS.warn, null, 1.6), el('span', null, '修订覆盖状态：STALE — 当前版本已不反映最新库存。'))
        panel.appendChild(note)
      }
    } else {
      const sec = el('div', 'insp-sec')
      sec.append(el('div', 'insp-h', '当前选择'), el('span', null, '选择左侧专辑批次或其中的组件查看详情。'))
      panel.appendChild(sec)
    }

    panel.appendChild(renderRevisionList(ws))
    return panel
  }

  // ── sticky summary / actions ───────────────────────────────────────
  function renderActionBar(ws) {
    const bar = el('footer', 'e-bar')
    bar.dataset.odId = 'action-bar'
    const summary = el('div', 'e-summary')
    const actionableAlbums = ws.folders.filter((f) => f.conclusion === 'ACTIONABLE' || f.conclusion === 'PARTIAL').length
    const blockedAlbums = ws.folders.filter((f) => f.conclusion === 'BLOCKED').length
    summary.append(
      el('span', null, ws.summary.albums + ' 个专辑 · '),
      el('span', 'cnt', String(actionableAlbums)),
      el('span', null, ' 个可执行 · '),
      el('span', 'cnt danger', String(blockedAlbums)),
      el('span', null, ' 个阻止 · '),
      el('span', 'cnt', String(ws.summary.gen + ws.summary.rem)),
      el('span', null, ' 项文件变化'),
    )
    bar.appendChild(summary)
    const actions = el('div', 'e-actions')
    const exec = el('button', 'btn btn-primary')
    exec.type = 'button'
    exec.disabled = true
    exec.setAttribute('aria-disabled', 'true')
    exec.title = '当前版本暂不支持执行 Workflow 计划'
    exec.append(svg(ICONS.lock), el('span', null, '执行能力待接入'))
    actions.appendChild(exec)
    bar.appendChild(actions)
    return bar
  }

  // ── libraries / create-workset stub (FUTURE-CONTRACT) ──────────────
  function renderLib() {
    const root = el('div', 'workbench')
    const g = renderGlobalHeader()
    root.appendChild(g)
    const head = el('header', 'ws-header')
    const crumb = el('div', 'crumb')
    const t = el('div', 'crumb-title')
    t.append(el('span', 't', '媒体库 · 选择专辑文件夹'), el('span', 'sep', '/'), el('span', 'id', '返回媒体库示意'))
    crumb.appendChild(t)
    head.appendChild(crumb)
    const right = el('div', 'header-right')
    const back = el('button', 'btn btn-ghost')
    back.type = 'button'
    back.append(el('span', null, '返回工作台'))
    back.addEventListener('click', () => { state.wsIdx = worksets.findIndex((w) => w.workset_id === 'ws_a1f2c3d4'); if (state.wsIdx < 0) state.wsIdx = 0; show('') })
    right.appendChild(back)
    head.appendChild(right)
    root.appendChild(head)
    root.appendChild(renderLibBody())
    return root
  }

  const PICKED = ['RJ01567288_夜間透過モデル', 'RJ01044712_アーカイブ Vol.2', 'RJ01277330_ボイスドラマ第2章', 'BGM_ost_extract', '音效_SE_pack_01', '音效_SE_pack_02', 'WorkArchive_2023']
  function renderLibBody() {
    const lp = el('div', 'libpanel')
    lp.dataset.odId = 'lib-jump-stub'
    if (state.libTarget && state.libTarget.path) {
      // jump-to-path mode (batch → file tree)
      lp.appendChild(el('div', 'lp-h', '媒体库文件树'))
      lp.appendChild(el('div', 'lp-p', '原型示意：按组件/批次路径定位的媒体库文件树，选中以下节点。实际路由为依据 library / folder 定位展开的 LibrariesPage。'))
      const tree = el('div', 'lib-tree')
      const parts = state.libTarget.path.split('/').filter(Boolean)
      parts.forEach((part, i) => {
        const node = el('div', 'lib-node' + (i === parts.length - 1 ? ' cur' : ''))
        node.append(svg(ICONS.folder, null, 1.6), el('span', i === parts.length - 1 ? null : 'n', part))
        tree.appendChild(node)
      })
      lp.appendChild(tree)
      return lp
    }
    // create-workset mode
    lp.appendChild(el('div', 'lp-h', 'Libraries → 新建工作集'))
    lp.appendChild(el('div', 'lp-p', '主流程：每轮选择一组专辑文件夹后创建新的工作集（FUTURE-CONTRACT · 无 Workset API 原型示意）。'))
    const picked = el('div', 'wc-picked')
    picked.appendChild(el('span', null, '已选择 ' + PICKED.length + ' 个专辑文件夹'))
    PICKED.forEach((p) => picked.appendChild(el('span', 'p', p)))
    lp.appendChild(picked)

    const box = el('div', 'ws-creator')
    box.appendChild(el('div', 'wc-h', '创建工作集'))
    box.appendChild(el('div', 'wc-s', '指定一个名称；创建后进入工作台，状态为「待规划」——不自动生成计划版本。'))
    const field = el('div', 'wc-field')
    field.append(el('label', null, '工作集名称'))
    const input = document.createElement('input')
    input.className = 'wc-input'
    input.setAttribute('aria-label', '工作集名称')
    input.value = ''
    input.placeholder = '夏季整理'
    field.appendChild(input)
    box.appendChild(field)
    if (!input.value) input.value = '夏季整理'
    const acts = el('div', 'wc-actions')
    const cancel = el('button', 'btn btn-ghost')
    cancel.type = 'button'
    cancel.append(el('span', null, '取消'))
    cancel.addEventListener('click', () => show(''))
    acts.appendChild(cancel)
    const create = el('button', 'btn btn-primary')
    create.type = 'button'
    create.append(el('span', null, '创建工作集'))
    create.title = 'FUTURE-CONTRACT · 创建后进入「待规划」状态'
    create.addEventListener('click', () => {
      // FUTURE-CONTRACT: client-side only, no server persistence claim
      const title = input.value.trim() || '未命名工作集'
      const wsIdx = worksets.push(makeDraftWorkset(title)) - 1
      state.wsIdx = wsIdx
      state.revIdx = 0
      state.workbenchStage = 'configure'
      state.filter = 'all'
      state.query = ''
      state.selBatch = null
      state.selComp = null
      const fb = worksets[wsIdx].folders[0]
      state.openBatches = new Set(fb ? [fb.folder_id] : [])
      state.selBatch = fb && fb.folder_id
      show('')
    })
    acts.appendChild(create)
    box.appendChild(acts)
    lp.appendChild(box)
    return lp
  }

  // FUNC-ONLY draft workset (pending state, no revision)
  function makeDraftWorkset(title) {
    const seedByDisplay = {
      RJ01567288_夜間透過モデル: F.rj015,
      'RJ01044712_アーカイブ Vol.2': F.rj010,
      'RJ01277330_ボイスドラマ第2章': F.rj012,
      BGM_ost_extract: F.bgm,
      音效_SE_pack_01: F.se01,
      音效_SE_pack_02: F.se02,
      WorkArchive_2023: F.w2023,
    }
    const folders = PICKED.map((disp, i) => pendingFolder(i, disp, seedByDisplay[disp]))
    const ws = {
      workset_id: 'ws_' + Math.random().toString(16).slice(2, 10),
      title,
      created_at: '2026-08-29 11:0' + (PICKED.length % 10) + ' (原型)',
      updated_at: '2026-08-29 11:0' + (PICKED.length % 10) + ' (原型)',
      folders,
      current_revision: null,
      revisions: [],
      workflow_draft: makeWorkflowDraft(),
    }
    ws.summary = wsSummary(ws)
    return ws
  }

  // ── full-page states ───────────────────────────────────────────────
  function renderFull(kind) {
    const panelEl = el('div', 'fullstate')
    panelEl.dataset.odId = 'full-' + kind
    const inner = el('div', 'panel')
    if (kind === 'not-found') {
      inner.append(el('h2', null, '找不到该工作集'), el('p', null, '它可能已被删除。'))
      const btns = el('div', 'btns')
      const back = el('button', 'btn btn-ghost')
      back.type = 'button'
      back.append(el('span', null, '返回工作集列表'))
      back.addEventListener('click', () => { state.wsIdx = 0; show('') })
      btns.appendChild(back)
      inner.appendChild(btns)
    } else {
      inner.appendChild(el('h2', null, '无法加载当前计划版本'))
      const p = el('p')
      p.append(el('span', 'code', 'REVISION_LOAD_FAILED'), el('span', null, ' — 工作集及其专辑文件夹仍然可用。'))
      inner.appendChild(p)
      const btns = el('div', 'btns')
      const retry = el('button', 'btn btn-ghost')
      retry.type = 'button'
      retry.append(svg(ICONS.refresh), el('span', null, '重试'))
      retry.addEventListener('click', () => show(''))
      btns.appendChild(retry)
      inner.appendChild(btns)
    }
    panelEl.appendChild(inner)
    return panelEl
  }

  function renderLoading() {
    const root = el('div', 'workbench')
    root.dataset.odId = 'view-loading'
    const g = el('header', 'g-header')
    for (let i = 0; i < 3; i++) g.appendChild(el('div', 'skel sk-line short'))
    root.appendChild(g)
    const body = el('div', 'stage-body')
    const feed = el('div', 'd-feed')
    for (let i = 0; i < 5; i++) feed.appendChild(el('div', 'skel sk-line short'))
    body.appendChild(feed)
    const main = el('div', 'main')
    for (let i = 0; i < 4; i++) main.appendChild(el('div', 'skel sk-line short'))
    const rowEl = el('div', 'body-row')
    const bp = el('div', 'd-batch')
    for (let i = 0; i < 3; i++) bp.appendChild(el('div', 'skel sk-row'))
    rowEl.appendChild(bp)
    const insp = el('div', 'd-inspector')
    for (let i = 0; i < 5; i++) insp.appendChild(el('div', 'skel sk-line'))
    rowEl.appendChild(insp)
    main.appendChild(rowEl)
    body.appendChild(main)
    root.appendChild(body)
    return root
  }

  // ── render dispatch ────────────────────────────────────────────────
  function actionBarSkeleton() {
    const bar = el('footer', 'e-bar')
    for (let i = 0; i < 3; i++) bar.appendChild(el('div', 'skel sk-line short'))
    return bar
  }

  function rerender(opts) {
    opts = opts || {}
    const view = location.hash.replace('#', '')
    if (view === 'lib') { app.replaceChildren(renderLib()); return }
    if (view === 'loading') {
      const r = renderLoading()
      r.appendChild(actionBarSkeleton())
      app.replaceChildren(r)
      return
    }
    if (view === 'not-found' || view === 'revision-failed') {
      const root = el('div', 'workbench')
      root.append(renderGlobalHeader())
      if (view === 'not-found') {
        const stage = el('div', 'stage-body')
        const feed = renderFeedEmpty()
        const main = el('div', 'main')
        main.appendChild(renderFull(view))
        stage.append(feed, main)
        root.appendChild(stage)
      } else {
        // revision load failed → keep workset + batches, replace only content
        const ws = curWs()
        const stage = el('div', 'stage-body')
        stage.appendChild(renderFeed())
        const main = el('div', 'main')
        main.appendChild(renderWsHeader(ws))
        main.appendChild(renderStage(ws))
        main.appendChild(renderRibbon(ws))
        const rowEl = el('div', 'body-row')
        rowEl.appendChild(renderBatchPane(ws))
        rowEl.appendChild(renderFull(view))
        main.appendChild(rowEl)
        main.appendChild(renderActionBar(ws))
        stage.appendChild(main)
        root.appendChild(stage)
      }
      app.replaceChildren(root)
      return
    }

    const ws = curWs()
    const root = el('div', 'workbench')
    root.appendChild(renderGlobalHeader())

    // 工作集 Feed 独占整个左列：header/stage/ribbon 移入 main 顶部，
    // stage-body 只有 [feed(全高) | main]。
    const body = el('div', 'stage-body')
    body.appendChild(state.wsIdx >= 0 && worksets.length ? renderFeed() : renderFeedEmpty())
    const main = el('div', 'main')
    if (state.revIdx > 0) {
      // FUTURE-CONTRACT read-back of a historical revision: metadata switches,
      // review content stays current — labelled, not faked.
      const rev = ws.revisions[state.revIdx]
      const b = el('div', 'view-banner')
      b.dataset.odId = 'history-readback'
      b.append(
        svg(ICONS.warn, null, 1.7),
        el('span', null, ''),
        el('span', 'code', rev.plan_id),
        el('span', null, '历史版本回看 · 元信息已切换，组件明细仍为当前快照（FUTURE-CONTRACT）'),
      )
      main.appendChild(b)
    }
    if (ws.current_revision && ws.current_revision.status === 'stale' && state.revIdx === 0) {
      const b = el('div', 'stale-banner')
      b.dataset.odId = 'stale-warning'
      b.append(svg(ICONS.warn), el('span', null, '当前计划版本已过期 — 文件库存或工作集范围发生变化，需要生成新版本。'))
      main.appendChild(b)
    }
    main.appendChild(renderWsHeader(ws))
    main.appendChild(renderStage(ws))
    if (state.workbenchStage === 'configure') {
      main.appendChild(renderComposer(ws))
      main.appendChild(renderComposeActions(ws))
    } else {
      main.appendChild(renderRibbon(ws))
      const rowEl = el('div', 'body-row')
      rowEl.appendChild(renderBatchPane(ws))
      rowEl.appendChild(renderInspector(ws))
      main.appendChild(rowEl)
      main.appendChild(renderActionBar(ws))
    }
    body.appendChild(main)
    root.appendChild(body)
    app.replaceChildren(root)
    if (state.flash) app.appendChild(renderGenToast())
    if (opts.focusSearch) {
      const s = app.querySelector('.ws-search input')
      if (s) { s.focus(); s.setSelectionRange(s.value.length, s.value.length) }
    }
  }

  // transient notice after generating a revision (原型的诚实反馈：模拟成功)
  function renderGenToast() {
    const f = state.flash
    const toast = el('div', 'gen-toast')
    toast.dataset.odId = 'generate-toast'
    toast.append(svg(ICONS.playFuture, null, 1.8))
    toast.append(el('span', null, f.kind === 'regenerated' ? '已重新生成计划版本' : '已生成计划版本'))
    toast.append(el('span', 'mono', f.planId))
    toast.append(el('span', null, ' · ' + f.count + ' 个专辑批次已纳入快照'))
    return toast
  }

  function renderFeedEmpty() {
    const feed = el('aside', 'd-feed')
    feed.dataset.odId = 'workset-feed'
    const head = el('div', 'feed-head')
    const t = el('div', 'fh-t')
    t.append(el('span', 't', '工作集'), el('span', 'sub', worksets.length + ' 条'))
    head.appendChild(t)
    feed.appendChild(head)
    const empty = el('div', 'feed-empty')
    empty.append(el('div', 'bh', '还没有工作集'), el('div', 'bp', '前往媒体库选择一组专辑文件夹，开始一次整理工作。'))
    feed.appendChild(empty)
    return feed
  }

  function show(view) {
    if (view !== '') { location.hash = view; return }
    if (location.hash) location.hash = ''
    rerender()
  }

  window.addEventListener('hashchange', () => rerender())

  // boot
  rerender()
