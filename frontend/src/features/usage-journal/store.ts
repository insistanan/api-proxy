import { defineStore } from 'pinia'
import { shallowRef } from 'vue'
import { apiRequest } from '@/shared/services/http'
import type { JournalDay, JournalDetail, JournalStatus } from '@/types/usage-journal'

export const useUsageJournal = defineStore('usage-journal', () => {
  const status = shallowRef<JournalStatus>()
  const days = shallowRef<JournalDay[]>([])
  const detail = shallowRef<JournalDetail>()

  async function loadStatus() {
    const snapshot = await apiRequest<JournalStatus>('/usage-journal/status')
    if (!status.value?.enabledAt || snapshot.enabledAt) status.value = snapshot
    return status.value
  }

  async function load(year?: number, date?: string) {
    const snapshot = await apiRequest<JournalStatus>('/usage-journal/status')
    if (!snapshot.enabledAt) {
      status.value = snapshot
      days.value = []
      detail.value = undefined
      return
    }
    const activeYear = year ?? Number(snapshot.today.slice(0, 4))
    const activeDate = date || (String(activeYear) === snapshot.today.slice(0, 4) ? snapshot.today : `${activeYear}-12-31`)
    const [calendar, ticket] = await Promise.all([
      apiRequest<JournalDay[]>(`/usage-journal/calendar?year=${activeYear}`),
      apiRequest<JournalDetail>(`/usage-journal/days/${encodeURIComponent(activeDate)}`)
    ])
    status.value = snapshot
    days.value = calendar
    detail.value = ticket
  }

  async function enable(timeZone: string) {
    status.value = await apiRequest<JournalStatus>('/usage-journal/enable', {
      method: 'POST', body: JSON.stringify({ timeZone })
    })
    await load()
  }

  async function select(date: string) {
    detail.value = await apiRequest<JournalDetail>(`/usage-journal/days/${encodeURIComponent(date)}`)
  }

  return { status, days, detail, loadStatus, load, enable, select }
})
