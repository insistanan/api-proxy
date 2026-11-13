<template>
  <v-dialog v-model="open" max-width="440" :persistent="loading">
    <v-card>
      <v-card-title>{{ title }}</v-card-title>
      <v-card-text class="confirm-dialog__message">{{ message }}</v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn variant="text" :disabled="loading" @click="open = false">{{ cancelText }}</v-btn>
        <v-btn :color="confirmColor" :loading="loading" @click="emit('confirm')">{{ confirmText }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
const open = defineModel<boolean>({ default: false })

withDefaults(defineProps<{
  message: string
  title?: string
  confirmText?: string
  cancelText?: string
  confirmColor?: string
  loading?: boolean
}>(), {
  title: '请确认',
  confirmText: '确定',
  cancelText: '取消',
  confirmColor: 'error',
  loading: false
})

const emit = defineEmits<{ confirm: [] }>()
</script>

<style scoped>
.confirm-dialog__message { white-space: pre-wrap; line-height: 1.7; }
</style>
