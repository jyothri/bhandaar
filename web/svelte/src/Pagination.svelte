<script lang="ts">
  import { createEventDispatcher, afterUpdate } from "svelte";
  export let page: number;
  export let maxPages: number;

  const dispatch = createEventDispatcher();
  const loadPreviousPage = () => dispatch("loadPreviousPage");
  const loadNextPage = () => dispatch("loadNextPage");

  let hasPrev: boolean;
  let hasNext: boolean;

  afterUpdate(() => {
    if (page == maxPages) {
      hasNext = false;
    } else {
      hasNext = true;
    }
    if (page <= 1) {
      hasPrev = false;
    } else {
      hasPrev = true;
    }
  });
</script>

<div>
  <span>Page {page} of {maxPages}</span>
  <button on:click={loadPreviousPage} disabled={!hasPrev}>previous</button>
  <button on:click={loadNextPage} disabled={!hasNext}>next</button>
</div>
