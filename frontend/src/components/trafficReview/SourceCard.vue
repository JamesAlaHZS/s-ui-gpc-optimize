<template>
  <v-card
    rounded="lg"
    variant="outlined"
  >
    <v-card-title class="d-flex align-center justify-space-between flex-wrap ga-2">
      <span>{{ t(`trafficReview.sources.${endpoint}`) }}</span>
      <v-chip
        size="small"
        :color="state.error ? 'warning' : state.updatedAt ? 'success' : 'default'"
      >
        {{ state.error ? t('trafficReview.stale') : state.updatedAt ? t('trafficReview.current') : t('trafficReview.waiting') }}
      </v-chip>
    </v-card-title>
    <v-card-text>
      <v-alert
        v-if="state.error"
        type="warning"
        density="compact"
        variant="tonal"
        class="mb-3"
      >
        {{ t('trafficReview.requestFailed') }}
      </v-alert>
      <p class="text-body-medium text-medium-emphasis mb-2">
        {{ state.updatedAt ? `${t('trafficReview.updatedAt')} ${new Date(state.updatedAt).toLocaleString(locale === 'zhHans' ? 'zh-CN' : locale === 'zhHant' ? 'zh-TW' : locale)}` : t('trafficReview.noData') }}
      </p>
      <slot :data="state.data">
        <p class="text-body-medium">
          {{ t('trafficReview.noMeasurements') }}
        </p>
      </slot>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { TrafficReviewEndpoint, TrafficReviewState } from '@/types/trafficReview'

defineProps<{ endpoint: TrafficReviewEndpoint, state: TrafficReviewState }>()
const { t, locale } = useI18n()
</script>
