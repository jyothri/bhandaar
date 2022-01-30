<script lang="ts">
  import { onMount } from "svelte";
  import ListScanData from "./ListScanData.svelte";
  import Pagination from "./Pagination.svelte";

  interface OptionalTime {
    Time: string;
    Valid: boolean;
  }

  interface Scans {
    scan_id: number;
    ScanType: string;
    CreatedOn: string;
    ScanStartTime: string;
    ScanEndTime: OptionalTime;
    Metadata: string;
  }

  const pageSize = 10;
  const apiEndpoint = "http://localhost:8090";
  let scanId = 0;
  let scans: Scans[] = [];
  let totalScans = 0;
  let page = 1;
  let status = "loading...";
  let heading = "Scans";
  let maxPages = 0;

  let loadScanData = async (scan_row: number) => {
    heading = `Scan Data for ${scan_row}`;
    scanId = scan_row;
    scans = [];
    status = "";
  };

  let fetchListScans = async () => {
    try {
      const res = await fetch(`${apiEndpoint}/api/scans?page=${page}`);
      let response = await res.json();
      totalScans = response.pagination_info.size;
      if (totalScans == 0) {
        status = "no data";
        return;
      }
      scans = response.scans;
      maxPages = 1 + Math.trunc(totalScans / pageSize);
      if (totalScans % pageSize == 0) {
        maxPages--;
      }
    } catch (err) {
      status = "error getting results";
    }
  };

  let loadPreviousPage = async () => {
    page--;
    await fetchListScans();
  };

  let loadNextPage = async () => {
    page++;
    await fetchListScans();
  };

  onMount(async () => {
    await fetchListScans();
  });
</script>

<h1>{heading}</h1>
<ListScanData bind:scanId />

{#if scans.length > 0}
  <Pagination
    {page}
    {maxPages}
    on:loadNextPage={loadNextPage}
    on:loadPreviousPage={loadPreviousPage}
  />
  <table>
    <tr>
      <th>id</th>
      <th>Scan type</th>
      <th>Created On</th>
      <th>Start Time</th>
      <th>End Time</th>
      <th>Metadata</th>
    </tr>
    {#each scans as scan}
      <tr on:click={() => loadScanData(scan.scan_id)}>
        <td>{scan.scan_id}</td>
        <td>{scan.ScanType}</td>
        <td>{scan.CreatedOn}</td>
        <td>{scan.ScanStartTime}</td>
        {#if scan.ScanEndTime.Valid}
          <td>{scan.ScanEndTime.Time}</td>
        {:else}
          <td> - </td>
        {/if}
        <td>{scan.Metadata}</td>
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
