<script lang="ts">
  let { size = 32 }: { size?: number } = $props();
</script>
<svg width={size} height={size} viewBox="0 0 40 40" fill="none" aria-hidden="true">
  <path d="M20 10v20M10 20h20M13 13l14 14M13 27l14-14" stroke="var(--accent)" stroke-width="2.3" stroke-linecap="round" />
  <path d="M20 10l-5-5M20 10l5-5M10 20l-5-5M10 20l-5 5M30 20l5-5M30 20l5 5M20 30l-5 5M20 30l5 5" stroke="var(--accent)" stroke-width="2.3" stroke-linecap="round" />
  <circle cx="20" cy="20" r="6.5" fill="var(--accent)" />
  <circle cx="18" cy="19" r="1.1" fill="white" /><circle cx="22" cy="19" r="1.1" fill="white" />
</svg>
