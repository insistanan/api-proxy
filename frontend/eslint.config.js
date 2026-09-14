import js from '@eslint/js'
import pluginVue from 'eslint-plugin-vue'
import vueParser from 'vue-eslint-parser'
import tsParser from '@typescript-eslint/parser'
import tsPlugin from '@typescript-eslint/eslint-plugin'
import prettierConfig from 'eslint-config-prettier'

export default [
  // 基础 JavaScript 推荐规则
  js.configs.recommended,

  // Vue 3 推荐规则
  ...pluginVue.configs['flat/recommended'],

  // Prettier 配置（禁用与 Prettier 冲突的规则）
  prettierConfig,

  // 全局配置
  {
    files: ['**/*.{js,ts,vue}'],
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'module',
      globals: {
        // 浏览器环境
        window: 'readonly',
        document: 'readonly',
        navigator: 'readonly',
        console: 'readonly',
        localStorage: 'readonly',
        sessionStorage: 'readonly',
        setTimeout: 'readonly',
        setInterval: 'readonly',
        clearTimeout: 'readonly',
        clearInterval: 'readonly',
        confirm: 'readonly',
        alert: 'readonly',
        prompt: 'readonly',
        fetch: 'readonly',
        URL: 'readonly',
        URLSearchParams: 'readonly',
        AbortController: 'readonly',
        AbortSignal: 'readonly',
        RequestInit: 'readonly',
        Response: 'readonly',
        Request: 'readonly',
        Headers: 'readonly',
        FormData: 'readonly',
        Blob: 'readonly',
        File: 'readonly',
        Event: 'readonly',
        KeyboardEvent: 'readonly',
        MouseEvent: 'readonly',
        // DOM API（按 lint 实际使用补全，R6 环境声明修正）
        HTMLElement: 'readonly',
        Element: 'readonly',
        PointerEvent: 'readonly',
        CustomEvent: 'readonly',
        FileReader: 'readonly',
        DOMException: 'readonly',
        TextDecoder: 'readonly',
        TextEncoder: 'readonly',
        Notification: 'readonly',
        AudioContext: 'readonly',
        requestAnimationFrame: 'readonly',
        cancelAnimationFrame: 'readonly',
        performance: 'readonly',
        crypto: 'readonly',
        getComputedStyle: 'readonly',
        matchMedia: 'readonly',
        history: 'readonly',
        location: 'readonly',
        addEventListener: 'readonly',
        removeEventListener: 'readonly'
      }
    }
  },

  // Node 构建脚本（scripts/*.mjs）：Node 环境全局（R6 环境声明修正）
  {
    files: ['scripts/**/*.mjs'],
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'module',
      globals: {
        console: 'readonly',
        process: 'readonly',
        URL: 'readonly',
        URLSearchParams: 'readonly',
        TextDecoder: 'readonly'
      }
    }
  },

  // TypeScript 文件配置
  {
    files: ['**/*.ts'],
    languageOptions: {
      parser: tsParser,
      parserOptions: {
        ecmaVersion: 'latest',
        sourceType: 'module'
      }
    },
    plugins: {
      '@typescript-eslint': tsPlugin
    },
    rules: {
      ...tsPlugin.configs.recommended.rules,
      // TypeScript 特定规则
      '@typescript-eslint/no-explicit-any': 'warn',
      '@typescript-eslint/no-unused-vars': [
        'warn',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_'
        }
      ],
      // TS 文件上关闭基础 no-unused-vars（TS-ESLint 官方要求）：
      // 基础规则不识别类型语法，对 interface 方法参数（API 契约文档）误报；
      // @typescript-eslint/no-unused-vars 带 AST 类型信息，接替全量检查。
      'no-unused-vars': 'off'
    }
  },

  // Vue 文件配置
  {
    files: ['**/*.vue'],
    languageOptions: {
      parser: vueParser,
      parserOptions: {
        ecmaVersion: 'latest',
        sourceType: 'module',
        parser: tsParser,
        extraFileExtensions: ['.vue']
      }
    },
    rules: {
      // Vue 3 特定规则
      'vue/multi-word-component-names': 'off', // 允许单词组件名
      'vue/no-v-html': 'warn', // 警告使用 v-html
      'vue/require-default-prop': 'off', // 不强制要求 prop 默认值
      'vue/require-explicit-emits': 'warn', // 建议显式声明 emits
      // 代码风格（与 Prettier 不冲突的规则）
      'vue/html-self-closing': [
        'error',
        {
          html: {
            void: 'always',
            normal: 'never',
            component: 'always'
          },
          svg: 'always',
          math: 'always'
        }
      ],
      'vue/max-attributes-per-line': 'off', // 由 Prettier 处理
      'vue/singleline-html-element-content-newline': 'off', // 由 Prettier 处理
      'vue/html-indent': 'off' // 由 Prettier 处理
    }
  },

  // 通用规则
  {
    rules: {
      // 代码质量
      'no-console': process.env.NODE_ENV === 'production' ? 'warn' : 'off',
      'no-debugger': process.env.NODE_ENV === 'production' ? 'error' : 'off',
      // TS 文件（含 .vue 的 script）由 @typescript-eslint/no-unused-vars 接管：
      // 基础规则不识别类型语法，对 interface 方法参数（API 契约文档）误报。
      // 纯 JS（scripts/*.mjs）不经过 TS 配置块，保留基础规则。
      'no-unused-vars': 'off',
      'no-undef': 'error',
      'prefer-const': 'warn',
      'no-var': 'error',

      // 代码风格（不与 Prettier 冲突）
      'no-multiple-empty-lines': [
        'error',
        {
          max: 1,
          maxEOF: 0
        }
      ],
      'eol-last': ['error', 'always']
    }
  },

  // 纯 JS 构建脚本：不经 TS 配置块，恢复基础 no-unused-vars。
  {
    files: ['scripts/**/*.mjs'],
    rules: {
      'no-unused-vars': [
        'warn',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_'
        }
      ]
    }
  },

  // 忽略文件
  {
    ignores: [
      'dist/**',
      'node_modules/**',
      '*.config.js',
      '*.config.ts',
      'coverage/**',
      '.vite/**',
      '.nuxt/**',
      '.output/**'
    ]
  }
]
