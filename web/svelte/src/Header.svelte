<script lang="ts">
  export let heading: string;
</script>

<h1>{heading}</h1>

<style>
  h1 {
    color: #0015ff;
    text-transform: uppercase;
    font-size: 4em;
    font-weight: 100;
    text-align: center;
  }

  @media (min-width: 640px) {
    h1 {
      max-width: none;
    }
  }
</style>
