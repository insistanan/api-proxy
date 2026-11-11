import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { VersionInfo } from '@/services/version'

/** 系统级只读信息：当前版本与更新检查结果，由顶栏版本徽标写入。 */
export const useSystemStore = defineStore('system', () => {
  const versionInfo = ref<VersionInfo>({
    currentVersion: '',
    latestVersion: null,
    isLatest: false,
    hasUpdate: false,
    releaseUrl: null,
    lastCheckTime: 0,
    status: 'checking'
  })

  function setVersionInfo(info: VersionInfo) {
    versionInfo.value = info
  }

  function setCurrentVersion(version: string) {
    versionInfo.value.currentVersion = version
  }

  return { versionInfo, setVersionInfo, setCurrentVersion }
})
