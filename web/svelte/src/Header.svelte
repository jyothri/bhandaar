<script lang="ts">
  export let heading: string;
</script>

<h1>{heading}</h1>

<style>
  h1 {
    color: #ff3e00;
    text-transform: uppercase;
    font-size: 4em;
    font-weight: 100;
  }

  @media (min-width: 640px) {
    h1 {
      max-width: none;
    }
  }
</style>
