<script>
  export let tiers = [{ name: "basic", pence: 100 }];
  $: total = tiers.reduce((n, t) => n + t.pence, 0);
</script>

<ul>
  {#each tiers as t}
    <li>{t.name}: {t.pence}</li>
  {/each}
</ul>
<p>Total {total}</p>

<style>
  p { font-weight: bold; }
</style>
