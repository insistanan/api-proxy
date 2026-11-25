export interface JournalStatus {
  enabledAt: string | null
  timeZone: string
  today: string
  syncIssue: boolean
}

export interface JournalDay {
  date: string
  tokens: number
  coverage: 'unrecorded' | 'partial' | 'open' | 'complete'
}

export interface JournalUsage {
  input: number
  output: number
  cacheCreation: number
  cacheRead: number
  attempts: number
}

export interface JournalDetail extends JournalDay {
  upstream: JournalUsage
  estimated: JournalUsage
  missingAttempts: number
  models: Array<{ name: string; tokens: number }>
  peakHour: string | null
  updatedAt: string | null
  timeZone: string
  revision: number
  lateUpdate: boolean
}
