<template>
  <div style="height:240px">
    <line-chart
      :data="data"
      :options="options"
      :aria-label="label"
      role="img"
    />
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { Chart as ChartJS, CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Filler } from 'chart.js'
import { Line as LineChart } from 'vue-chartjs'
import type { TrafficSeriesPoint } from '@/types/trafficReview'
ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Filler)
const props = defineProps<{ points: TrafficSeriesPoint[], timezone: string, locale: string, label: string }>()
const spansDays = computed(() => props.points.length > 1 && props.points[props.points.length - 1]!.time - props.points[0]!.time >= 86400)
const data = computed(() => ({
  labels: props.points.map(point => new Intl.DateTimeFormat(props.locale, { ...(spansDays.value ? { year: 'numeric' as const, month: '2-digit' as const, day: '2-digit' as const } : {}), hour: '2-digit', minute: '2-digit', timeZone: props.timezone }).format(new Date(point.time * 1000))),
  datasets: [{ label: props.label, data: props.points.map(point => point.txBps == null || point.quality !== 'ok' ? null : point.txBps / 1024 ** 2), borderColor: '#4278e8', backgroundColor: '#4278e820', fill: true, pointRadius: 0, borderWidth: 2, spanGaps: false }],
}))
const options = { responsive: true, maintainAspectRatio: false, animation: false as const, scales: { x: { ticks: { maxTicksLimit: 8 }, grid: { display: false } }, y: { beginAtZero: true, title: { display: true, text: 'MiB/s' } } }, plugins: { legend: { display: false } } }
</script>
