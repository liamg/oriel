<template>
  <ul class="tiers">
    <li v-for="t in tiers" :key="t.name">{{ t.name }}: {{ price(t) }}</li>
  </ul>
</template>

<script setup>
const tiers = [{ name: "basic", pence: 100 }, { name: "pro", pence: 450 }];
const price = (t) => (t.pence / 100).toFixed(2);
</script>

<style scoped>
.tiers { color: #333; }
</style>
