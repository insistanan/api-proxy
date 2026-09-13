import { createVuetify } from 'vuetify'
import { h } from 'vue'
import type { IconSet, IconProps, ThemeDefinition } from 'vuetify'
import * as components from 'vuetify/components'
import * as directives from 'vuetify/directives'

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
  mdiApplicationExport,
  mdiFolderOpen,
  mdiShieldLock,
  mdiEyeOff,
  mdiEyeCheck,
  mdiShieldCheck,
  mdiBellRing,
  mdiRegex
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
  menu: mdiMenuDown,
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
  account: mdiAccount,
  'account-group-outline': mdiAccountGroupOutline,
  send: mdiSend,
  download: mdiDownload,
  'application-export': mdiApplicationExport,
  brain: mdiBrain,
  wrench: mdiWrench,
  replay: mdiReplay,
  'code-json': mdiCodeJson,
  'folder-open': mdiFolderOpen
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

// Light Theme — 雾灰底 + 纯白卡片，深靛主色在白底上足够沉，长时间看不累
const lightTheme: ThemeDefinition = {
  dark: false,
  colors: {
    primary: '#3E4DA8',
    'primary-darken-1': '#2F3C8B',
    'primary-lighten-1': '#6774CB',
    secondary: '#586170',
    accent: '#A55E3C',

    info: '#1F6E8C',
    success: '#0E7551',
    warning: '#A56A12',
    error: '#B63E3E',

    background: '#E9EBF1',
    surface: '#FFFFFF',
    'surface-variant': '#E1E5ED',
    'surface-bright': '#FFFFFF',
    'on-surface': '#1D222C',
    'on-background': '#252B36',
    'on-surface-variant': '#555E6E',
    outline: '#939CAC',
    'outline-variant': '#C9CFDA'
  }
}

// Dark Theme — 近黑石墨底 + 提亮卡片，文字用暖白而非纯白，避免暗背景上的眩光
const darkTheme: ThemeDefinition = {
  dark: true,
  colors: {
    primary: '#8E9BE8',
    'primary-darken-1': '#7683DC',
    'primary-lighten-1': '#ABB4F1',
    secondary: '#A0A8B6',
    accent: '#D18C68',

    info: '#63B0CE',
    success: '#43C48D',
    warning: '#DDA155',
    error: '#E97C7C',

    background: '#090C12',
    surface: '#151A22',
    'surface-variant': '#1F252F',
    'surface-bright': '#28303C',
    'on-surface': '#E7EAF1',
    'on-background': '#DBDFE8',
    'on-surface-variant': '#A2AAB8',
    outline: '#5B6472',
    'outline-variant': '#2B323D'
  }
}

export default createVuetify({
  components,
  directives,
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
    VTextField: {
      variant: 'outlined',
      density: 'comfortable'
    },
    VTextarea: {
      variant: 'outlined',
      density: 'comfortable'
    },
    VSelect: {
      variant: 'outlined',
      density: 'comfortable'
    },
    VCombobox: {
      variant: 'outlined',
      density: 'comfortable'
    },
    VAutocomplete: {
      variant: 'outlined',
      density: 'comfortable'
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
