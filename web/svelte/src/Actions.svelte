<script lang="ts">
  let SubComponent: any;
  let loadListScans = async () => {
    SubComponent = null;
    SubComponent = (await import("./ListScans.svelte")).default;
  };
</script>

<button on:click={loadListScans}> Scan Info </button>
<button> Start a scan </button>
<hr />
<svelte:component this={SubComponent} />

<style>
  button {
    cursor: pointer;
    padding: 0.5rem 1.5rem;
    border-radius: 3.75rem;
    font-size: 1.2rem;
    font-weight: 600;
    margin: 0.4em;

    border: 1px solid #80016d;
    background-image: linear-gradient(-180deg, #8f89ff 0%, #b4b6cd 100%);
    box-shadow: 0 0.75rem 0.5rem rgba(255, 255, 255, 0.4) inset,
      0 0.25rem 0.5rem 0 rgba(180, 70, 207, 1) inset;
  }
</style>
