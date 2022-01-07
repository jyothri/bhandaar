<script lang="ts">
  import { onMount } from "svelte";

  interface OptionalTime {
    Time: string;
    Valid: boolean;
  }

  interface Scans {
    scan_id: string;
    ScanType: string;
    CreatedOn: string;
    ScanStartTime: string;
    ScanEndTime: OptionalTime;
  }

  let scans: Scans[] = [];
  let page = 1;
  let status = "loading...";

  let fetchListScans = async () => {
    try {
      const res = await fetch(`http://localhost:8090/api/scans?page=` + page);
      let response = await res.json();
      scans = response.scans;
    } catch (err) {
      status = "no results";
    }
  };

  onMount(async () => {
    await fetchListScans();
  });
</script>

<h1>Scans</h1>
{#if scans.length > 0}
  <table>
    <tr>
      <th>id</th>
      <th>Scan type</th>
      <th>Created On</th>
      <th>Start Time</th>
      <th>End Time</th>
    </tr>
    {#each scans as scan}
      <tr>
        <td>{scan.scan_id}</td>
        <td>{scan.ScanType}</td>
        <td>{scan.CreatedOn}</td>
        <td>{scan.ScanStartTime}</td>
        {#if scan.ScanEndTime.Valid}
          <td>{scan.ScanEndTime.Time}</td>
        {:else}
          <td> - </td>
        {/if}
      </tr>
    {/each}
  </table>
{:else}
  <!-- this block renders when there are no scans -->
  <p>{status}</p>
{/if}

<style>
  table {
    width: 100%;
    border: 1px solid black;
  }

  th,
  td {
    border: 1px solid black;
  }
</style>
