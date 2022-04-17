<script lang="ts">
  import { link } from "svelte-spa-router";
</script>

<a href="/" use:link> Scan Info </a>
<a href="/startScan" use:link> Start a scan </a>
<br />
<br />
<hr />

<style>
  a {
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
  a:link {
    text-decoration: none;
  }

  a:visited {
    text-decoration: none;
  }

  a:hover {
    text-decoration: none;
  }

  a:active {
    text-decoration: none;
  }
</style>
