import { defineStore } from 'pinia'
import { shallowRef } from 'vue'
import { apiRequest } from '@/shared/services/http'
import type { JournalDay, JournalDetail, JournalStatus } from '@/types/usage-journal'

export const useUsageJournal = defineStore('usage-journal', () => {
  const status = shallowRef<JournalStatus>()
  const days = shallowRef<JournalDay[]>([])
  const detail = shallowRef<JournalDetail>()
  let lifetime = 0
  let selection = 0

  async function loadStatus() {
    const generation = lifetime
    const snapshot = await apiRequest<JournalStatus>('/usage-journal/status')
    if (generation !== lifetime) return
    if (!status.value?.enabledAt || snapshot.enabledAt) status.value = snapshot
    return status.value
  }

  async function load(year?: number, date?: string) {
    const generation = lifetime
    const current = ++selection
    const snapshot = await apiRequest<JournalStatus>('/usage-journal/status')
    if (generation !== lifetime || current !== selection) return
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
    if (generation !== lifetime || current !== selection) return
    status.value = snapshot
    days.value = calendar
    detail.value = ticket
  }

  async function enable(timeZone: string) {
    const generation = lifetime
    const snapshot = await apiRequest<JournalStatus>('/usage-journal/enable', {
      method: 'POST', body: JSON.stringify({ timeZone })
    })
    if (generation !== lifetime) return
    status.value = snapshot
    await load()
  }

  async function select(date: string) {
    const generation = lifetime
    const current = ++selection
    const value = await apiRequest<JournalDetail>(`/usage-journal/days/${encodeURIComponent(date)}`)
    if (generation === lifetime && current === selection) detail.value = value
  }

  function reset() { lifetime++; selection++; status.value = undefined; days.value = []; detail.value = undefined }
  return { status, days, detail, loadStatus, load, enable, select, reset }
})
