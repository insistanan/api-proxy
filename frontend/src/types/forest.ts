export type TreeKind = 'usage' | 'focus' | 'rest'
export type TreeCondition = 'healthy' | 'damaged' | 'destroyed'

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
  accessory: string
  condition: TreeCondition
  conditionRevision: number
}

export interface ForestIsland {
  id: number
  name: string
  favorite: boolean
  count: number
  damaged: number
  destroyed: number
}

export interface ForestGroup {
  id: number
  islands: number
  trees: number
  damaged: number
  destroyed: number
}

export interface ForestEvent {
  sequence: number
  id: string
  island: number
  treeId: number
  at: number
  title: string
  story: string
  effects: ForestEventEffects
  affected?: Array<{ treeId: number; before: TreeCondition; after: TreeCondition }>
}

export interface ForestResources {
  wood: number
  seeds: number
  dew: number
}
export interface ForestEventEffects extends ForestResources {
  points: number
  damaged: number
  destroyed: number
  restored: number
  awayDays: number
  riskPercent: number
}
export interface ForestEcology {
  damaged: number
  destroyed: number
  repairCost: ForestResources
  replantCost: ForestResources
}
export interface ForestShopItem {
  id: string
  name: string
  description: string
  category: 'landscape' | 'species' | 'accessory'
  rarity: string
  price: number
  level: number
  owned: boolean
  cost: ForestResources
  kind?: TreeKind
  style: number
}
export interface ForestAchievement {
  id: string
  name: string
  description: string
  rarity: string
  target: number
  progress: number
  points: number
  unlockedAt: number
  treeId: number
}
export interface ForestEventFeed {
  instance: string
  events: ForestEvent[]
  cursor: number
  more: boolean
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
  ornaments: ForestShopItem[]
  resources: ForestResources
  achievements: ForestAchievement[]
  favorites: ForestIsland[]
  milestones: ForestEvent[]
  catchingUp: boolean
  syncIssue: boolean
  ecology: ForestEcology
}
