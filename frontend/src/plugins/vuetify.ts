import { createVuetify } from 'vuetify'
import { h } from 'vue'
import type { IconSet, IconProps, ThemeDefinition } from 'vuetify'
import * as components from 'vuetify/components'
import * as directives from 'vuetify/directives'
import { zhHans } from 'vuetify/locale'

// 引入样式
import 'vuetify/styles'

// 从 @mdi/js 按需导入使用的图标 (SVG)
import {
  mdiAccount,
  mdiSwapVerticalBold,
  mdiPlayCircle,
  mdiDragVertical,
  mdiOpenInNew,
  mdiKey,
  mdiRefresh,
  mdiDotsVertical,
  mdiPencil,
  mdiSpeedometer,
  mdiSpeedometerSlow,
  mdiRocketLaunch,
  mdiPauseCircle,
  mdiStopCircle,
  mdiDelete,
  mdiPlaylistRemove,
  mdiArchiveOutline,
  mdiPlus,
  mdiCheckCircle,
  mdiCheckCircleOutline,
  mdiBlockHelper,
  mdiCheckUnderlineCircle,
  mdiFlashAlert,
  mdiAlertCircle,
  mdiHelpCircle,
  mdiCloseCircle,
  mdiTag,
  mdiInformation,
  mdiCog,
  mdiWeb,
  mdiShieldAlert,
  mdiText,
  mdiSwapHorizontal,
  mdiArrowRight,
  mdiClose,
  mdiArrowLeft,
  mdiArrowUpBold,
  mdiArrowUpBoldCircleOutline,
  mdiArrowDownBold,
  mdiCheck,
  mdiAccountGroupOutline,
  mdiContentCopy,
  mdiContentSave,
  mdiAlert,
  mdiWeatherNight,
  mdiWhiteBalanceSunny,
  mdiLogout,
  mdiServerNetwork,
  mdiDatabasePlusOutline,
  mdiFilterRemoveOutline,
  mdiMagnifyClose,
  mdiTrayArrowDown,
  mdiImageMultipleOutline,
  mdiFileCogOutline,
  mdiTray,
  mdiProgressClock,
  mdiHeartPulse,
  mdiChevronDown,
  mdiChevronUp,
  mdiChevronLeft,
  mdiChevronRight,
  mdiTune,
  mdiRotateRight,
  mdiDice6,
  mdiBackupRestore,
  mdiPin,
  mdiPinOutline,
  mdiKeyChain,
  mdiRobot,
  mdiRobotOutline,
  mdiRobotOff,
  mdiMessageProcessing,
  mdiMessageReplyText,
  mdiDiamondStone,
  mdiApi,
  mdiLightningBolt,
  mdiLinkVariant,
  mdiFormTextbox,
  mdiMenuDown,
  mdiMenu,
  mdiMenuUp,
  mdiCheckboxMarked,
  mdiCheckboxBlankOutline,
  mdiMinusBox,
  mdiCircle,
  mdiRadioboxMarked,
  mdiRadioboxBlank,
  mdiStar,
  mdiStarOutline,
  mdiStarHalf,
  mdiPageFirst,
  mdiPageLast,
  mdiUnfoldMoreHorizontal,
  mdiLoading,
  mdiClockOutline,
  mdiChartLineVariant,
  mdiChartBoxOutline,
  mdiMagnify,
  mdiCalendar,
  mdiPaperclip,
  mdiEyedropper,
  mdiShieldRefresh,
  mdiShieldOffOutline,
  mdiAlertCircleOutline,
  mdiChartTimelineVariant,
  mdiChartAreaspline,
  mdiChartLine,
  mdiCodeBraces,
  mdiDatabase,
  mdiDatabaseImport,
  mdiFolderArrowDown,
  mdiPuzzleOutline,
  mdiTextBoxSearchOutline,
  mdiSignature,
  mdiImageSearchOutline,
  mdiHistory,
  mdiHelpCircleOutline,
  mdiChatOutline,
  mdiChatProcessing,
  mdiSend,
  mdiArrowCollapseUp,
  mdiArrowCollapseDown,
  mdiArchiveClockOutline,
  mdiFormatListBulleted,
  mdiSort,
  mdiTimerSand,
  mdiTestTube,
  mdiIncognito,
  mdiImage,
  mdiTranslate,
  mdiDownload,
  mdiClipboardCheckOutline,
  mdiFilterVariant,
  mdiPlaylistPlus,
  mdiCheckboxMultipleMarkedOutline,
  mdiBrain,
  mdiWrench,
  mdiReplay,
  mdiCodeJson,
  mdiFolderOpen,
  mdiShieldLock,
  mdiEyeOff,
  mdiEyeCheck,
  mdiBroom,
  mdiFileFindOutline,
  mdiShieldCheck,
  mdiBellRing,
  mdiRegex,
  mdiSourceBranch,
  mdiSync,
  mdiCloudDownloadOutline,
  mdiCloudUploadOutline
} from '@mdi/js'

// 图标名称到 SVG path 的映射 (使用 kebab-case)
const iconMap: Record<string, string> = {
  'playlist-plus': mdiPlaylistPlus,
  'checkbox-multiple-marked-outline': mdiCheckboxMultipleMarkedOutline,
  complete: mdiCheck,
  cancel: mdiCloseCircle,
  close: mdiClose,
  delete: mdiDelete,
  clear: mdiClose,
  success: mdiCheckCircle,
  info: mdiInformation,
  warning: mdiAlert,
  error: mdiAlertCircle,
  prev: mdiChevronLeft,
  next: mdiChevronRight,
  checkboxOn: mdiCheckboxMarked,
  checkboxOff: mdiCheckboxBlankOutline,
  checkboxIndeterminate: mdiMinusBox,
  delimiter: mdiCircle,
  sortAsc: mdiArrowUpBold,
  sortDesc: mdiArrowDownBold,
  expand: mdiChevronDown,
  menu: mdiMenu,
  subgroup: mdiMenuDown,
  dropdown: mdiMenuDown,
  radioOn: mdiRadioboxMarked,
  radioOff: mdiRadioboxBlank,
  edit: mdiPencil,
  ratingEmpty: mdiStarOutline,
  ratingFull: mdiStar,
  ratingHalf: mdiStarHalf,
  loading: mdiLoading,
  first: mdiPageFirst,
  last: mdiPageLast,
  unfold: mdiUnfoldMoreHorizontal,
  file: mdiPaperclip,
  magnify: mdiMagnify,
  plus: mdiPlus,
  minus: mdiMinusBox,
  calendar: mdiCalendar,
  treeviewCollapse: mdiMenuDown,
  treeviewExpand: mdiMenuUp,
  eyeDropper: mdiEyedropper,

  // 布局与导航
  'swap-vertical-bold': mdiSwapVerticalBold,
  'drag-vertical': mdiDragVertical,
  'open-in-new': mdiOpenInNew,
  'chevron-down': mdiChevronDown,
  'chevron-up': mdiChevronUp,
  'chevron-left': mdiChevronLeft,
  'chevron-right': mdiChevronRight,
  'dots-vertical': mdiDotsVertical,
  logout: mdiLogout,
  'archive-outline': mdiArchiveOutline,
  'archive-clock-outline': mdiArchiveClockOutline,
  'content-save': mdiContentSave,
  'menu-down': mdiMenuDown,
  'menu-up': mdiMenuUp,

  // 操作按钮
  pencil: mdiPencil,
  refresh: mdiRefresh,
  check: mdiCheck,
  'content-copy': mdiContentCopy,
  'arrow-up-bold': mdiArrowUpBold,
  'arrow-up-bold-circle-outline': mdiArrowUpBoldCircleOutline,
  'arrow-down-bold': mdiArrowDownBold,
  'arrow-right': mdiArrowRight,
  'arrow-left': mdiArrowLeft,
  'swap-horizontal': mdiSwapHorizontal,
  'rotate-right': mdiRotateRight,
  'backup-restore': mdiBackupRestore,

  // 状态图标
  'play-circle': mdiPlayCircle,
  'pause-circle': mdiPauseCircle,
  'stop-circle': mdiStopCircle,
  'check-circle': mdiCheckCircle,
  'check-circle-outline': mdiCheckCircleOutline,
  'block-helper': mdiBlockHelper,
  'check-underline-circle': mdiCheckUnderlineCircle,
  'flash-alert': mdiFlashAlert,
  'alert-circle': mdiAlertCircle,
  'alert-circle-outline': mdiAlertCircleOutline,
  'close-circle': mdiCloseCircle,
  'help-circle': mdiHelpCircle,
  alert: mdiAlert,

  'shield-refresh': mdiShieldRefresh,
  'shield-off-outline': mdiShieldOffOutline,
  'shield-lock': mdiShieldLock,
  'shield-check': mdiShieldCheck,
  'eye-off': mdiEyeOff,
  'eye-check': mdiEyeCheck,
  broom: mdiBroom,
  'file-find-outline': mdiFileFindOutline,
  'bell-ring': mdiBellRing,
  regex: mdiRegex,

  key: mdiKey,
  'key-chain': mdiKeyChain,
  speedometer: mdiSpeedometer,
  'speedometer-slow': mdiSpeedometerSlow,
  'rocket-launch': mdiRocketLaunch,
  'playlist-remove': mdiPlaylistRemove,
  tag: mdiTag,
  information: mdiInformation,
  cog: mdiCog,
  web: mdiWeb,
  'shield-alert': mdiShieldAlert,
  text: mdiText,
  tune: mdiTune,
  'dice-6': mdiDice6,
  'heart-pulse': mdiHeartPulse,
  'server-network': mdiServerNetwork,
  pin: mdiPin,
  'pin-outline': mdiPinOutline,
  'lightning-bolt': mdiLightningBolt,
  'link-variant': mdiLinkVariant,
  'form-textbox': mdiFormTextbox,
  'clock-outline': mdiClockOutline,
  'clipboard-check-outline': mdiClipboardCheckOutline,
  'filter-variant': mdiFilterVariant,
  'chart-line-variant': mdiChartLineVariant,
  'chart-box-outline': mdiChartBoxOutline,
  paperclip: mdiPaperclip,
  'eye-dropper': mdiEyedropper,

  // 主题切换
  'weather-night': mdiWeatherNight,
  'white-balance-sunny': mdiWhiteBalanceSunny,

  // 服务类型图标
  robot: mdiRobot,
  'robot-outline': mdiRobotOutline,
  'robot-off': mdiRobotOff,
  'message-processing': mdiMessageProcessing,
  'message-reply-text': mdiMessageReplyText,
  'chat-outline': mdiChatOutline,
  'chat-processing': mdiChatProcessing,
  'diamond-stone': mdiDiamondStone,
  api: mdiApi,
  image: mdiImage,
  translate: mdiTranslate,

  'checkbox-marked': mdiCheckboxMarked,
  'checkbox-blank-outline': mdiCheckboxBlankOutline,
  'minus-box': mdiMinusBox,
  'radiobox-marked': mdiRadioboxMarked,
  'radiobox-blank': mdiRadioboxBlank,
  star: mdiStar,
  'star-outline': mdiStarOutline,
  'star-half': mdiStarHalf,
  'page-first': mdiPageFirst,
  'page-last': mdiPageLast,
  'unfold-more-horizontal': mdiUnfoldMoreHorizontal,
  circle: mdiCircle,
  'chart-timeline-variant': mdiChartTimelineVariant,
  'chart-areaspline': mdiChartAreaspline,
  'chart-line': mdiChartLine,
  'code-braces': mdiCodeBraces,
  database: mdiDatabase,
  'database-import': mdiDatabaseImport,
  'folder-arrow-down': mdiFolderArrowDown,
  'puzzle-outline': mdiPuzzleOutline,
  'text-box-search-outline': mdiTextBoxSearchOutline,
  'database-plus-outline': mdiDatabasePlusOutline,
  'filter-remove-outline': mdiFilterRemoveOutline,
  'magnify-close': mdiMagnifyClose,
  'tray-arrow-down': mdiTrayArrowDown,
  'image-multiple-outline': mdiImageMultipleOutline,
  'file-cog-outline': mdiFileCogOutline,
  tray: mdiTray,
  'progress-clock': mdiProgressClock,
  signature: mdiSignature,
  'image-search-outline': mdiImageSearchOutline,
  history: mdiHistory,
  'help-circle-outline': mdiHelpCircleOutline,
  'arrow-collapse-up': mdiArrowCollapseUp,
  'arrow-collapse-down': mdiArrowCollapseDown,
  'format-list-bulleted': mdiFormatListBulleted,
  sort: mdiSort,
  'timer-sand': mdiTimerSand,
  'test-tube': mdiTestTube,
  incognito: mdiIncognito,
  account: mdiAccount,
  'account-group-outline': mdiAccountGroupOutline,
  send: mdiSend,
  download: mdiDownload,
  brain: mdiBrain,
  wrench: mdiWrench,
  replay: mdiReplay,
  'code-json': mdiCodeJson,
  'folder-open': mdiFolderOpen,
  'source-branch': mdiSourceBranch,
  sync: mdiSync,
  'cloud-download-outline': mdiCloudDownloadOutline,
  'cloud-upload-outline': mdiCloudUploadOutline
}

// 自定义 SVG iconset
const customSvgIconSet: IconSet = {
  component: (props: IconProps) => {
    let iconName = props.icon as string
    if (iconName.startsWith('mdi-')) {
      iconName = iconName.substring(4)
    }
    const svgPath = iconMap[iconName]

    if (!svgPath) {
      if (import.meta.env.DEV) {
        console.warn(`[Vuetify Icon] 未找到图标: ${iconName}`)
      }
      return h(
        'svg',
        {
          class: 'v-icon__svg v-icon__svg--missing',
          xmlns: 'http://www.w3.org/2000/svg',
          viewBox: '0 0 24 24',
          role: 'img',
          'aria-hidden': 'true',
          style: { fontSize: 'inherit', width: '1em', height: '1em' }
        },
        [h('path', { d: mdiHelpCircle, fill: 'currentColor' })]
      )
    }

    return h(
      'svg',
      {
        class: 'v-icon__svg',
        xmlns: 'http://www.w3.org/2000/svg',
        viewBox: '0 0 24 24',
        role: 'img',
        'aria-hidden': 'true',
        style: { fontSize: 'inherit', width: '1em', height: '1em' }
      },
      [h('path', { d: svgPath, fill: 'currentColor' })]
    )
  }
}

// ============================================================
// 色彩系统 — "砚与铜"：石墨冷底 + 深靛主色 + 铜赤点缀
//
// 设计取舍：
//   柔和 = 底色低饱和、文字避开纯黑/纯白、状态色统一压低明度纯度
//   强对比 = 靠 outline（边框）和 surface/background 的层差拉开模块边界，
//            而不是靠提高颜色饱和度——所以看着"狠"但不扎眼
// ============================================================

// Light Theme — 冷灰画布 + 纯白卡片，深靛主色在白底上足够沉，长时间看不累
const lightTheme: ThemeDefinition = {
  dark: false,
  colors: {
    primary: '#3E4DA8',
    'primary-darken-1': '#2F3C8B',
    'primary-lighten-1': '#6774CB',
    secondary: '#586170',
    accent: '#A55E3C',

    info: '#25648C',
    success: '#167055',
    warning: '#96650F',
    error: '#B23B45',

    background: '#F2F4F8',
    surface: '#FFFFFF',
    'surface-variant': '#E8ECF3',
    'surface-bright': '#FFFFFF',
    'on-surface': '#171B24',
    'on-background': '#1E232D',
    'on-surface-variant': '#5A6373',
    outline: '#8B94A5',
    'outline-variant': '#D2D8E2'
  }
}

// Dark Theme — 石墨层叠：画布与卡面靠明度差分层而不是阴影；
// 主色作实心按钮时改用浅靛底加深色字，保证正文对比度达标（白字在亮靛上只有 2.6:1）。
const darkTheme: ThemeDefinition = {
  dark: true,
  colors: {
    primary: '#8C99E6',
    'primary-darken-1': '#7784D8',
    'primary-lighten-1': '#A9B3EF',
    'on-primary': '#0C0F15',
    secondary: '#A0A8B6',
    accent: '#C98D6A',

    info: '#6FB3CE',
    success: '#4CC294',
    warning: '#D9A25E',
    error: '#E58A8A',

    background: '#0E1116',
    surface: '#161A22',
    'surface-variant': '#1E232C',
    'surface-bright': '#232A35',
    'on-surface': '#E8EBF2',
    'on-background': '#DDE1E9',
    'on-surface-variant': '#9BA4B3',
    outline: '#6B7484',
    'outline-variant': '#2C333E'
  }
}

export default createVuetify({
  components,
  directives,
  // 分页、无数据等组件内置文案使用简体中文。
  locale: { locale: 'zhHans', messages: { zhHans } },
  icons: {
    defaultSet: 'mdi',
    sets: { mdi: customSvgIconSet }
  },
  theme: {
    defaultTheme: 'light',
    themes: {
      light: lightTheme,
      dark: darkTheme
    },
    variations: {
      colors: ['primary', 'secondary', 'info', 'success', 'warning', 'error'],
      lighten: 3,
      darken: 2
    }
  },
  defaults: {
    VCard: {
      elevation: 0,
      rounded: 'md'
    },
    VBtn: {
      rounded: 'sm'
    },
    VChip: {
      rounded: 'sm'
    },
    VDialog: {
      rounded: 'md'
    },
    VMenu: {
      rounded: 'md'
    },
    VTooltip: {
      location: 'top'
    },
    // 仅在存在校验错误时展开详情，避免空提示区域占用表单空间。
    VTextField: {
      variant: 'outlined',
      density: 'comfortable',
      hideDetails: 'auto'
    },
    VTextarea: {
      variant: 'outlined',
      density: 'comfortable',
      hideDetails: 'auto'
    },
    VSelect: {
      variant: 'outlined',
      density: 'comfortable',
      hideDetails: 'auto'
    },
    VCombobox: {
      variant: 'outlined',
      density: 'comfortable',
      hideDetails: 'auto'
    },
    VAutocomplete: {
      variant: 'outlined',
      density: 'comfortable',
      hideDetails: 'auto'
    },
    VTable: {
      density: 'comfortable'
    },
    VList: {
      density: 'comfortable'
    },
    VExpansionPanel: {
      variant: 'accordion'
    }
  }
})
