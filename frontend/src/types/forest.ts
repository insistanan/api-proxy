export type TreeKind = 'usage' | 'focus' | 'rest'

export interface ForestTree {
  id: number
  kind: TreeKind
  style: number
  name: string
  island: number
  slot: number
  variant: number
  planted: number
  assetVersion: number
}

export interface ForestIsland {
  id: number
  name: string
  favorite: boolean
  count: number
}

export interface ForestGroup {
  id: number
  islands: number
  trees: number
}

export interface ForestEvent {
  id: string
  island: number
  title: string
  story: string
}

export interface ForestDay {
  trees: ForestTree[]
  counts: Record<TreeKind, number>
  events: ForestEvent[]
}

export interface ForestIslandView {
  island: ForestIsland
  trees: ForestTree[]
  events: ForestEvent[]
}

export interface ForestTimer {
  revision: number
  mode: '' | 'focus' | 'focus-due' | 'paused' | 'rest'
  started: number
  due: number
  duration: number
  left: number
  waitStarted: number
  waitDue: number
  serverNow: number
}

export interface ForestWorld {
  tokens: number
  seed: number
  seedGoal: number
  focus: number
  rest: number
  level: number
  levelProgress: number
  levelGoal: number
  baseline: number
  points: number
  balance: number
  islands: number
  counts: Record<TreeKind, number>
  discoveries: Array<{ kind: TreeKind; style: number; firstPlanted: number }>
  latest: ForestTree | null
  timer: ForestTimer
  ornaments: Array<{ id: string; name: string; price: number; level: number; owned: boolean }>
  favorites: ForestIsland[]
  milestones: ForestEvent[]
  catchingUp: boolean
  syncIssue: boolean
}
