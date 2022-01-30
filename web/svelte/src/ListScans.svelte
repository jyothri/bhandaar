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
    Duration: string;
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

  let deleteScan = async (scanId: number) => {
    try {
      await fetch(`${apiEndpoint}/api/scans/${scanId}`, {
        method: "DELETE",
      });
      status = "Deleted scan " + scanId;
      const index = scans.findIndex((x) => x.scan_id == scanId);
      if (index > -1) {
        scans.splice(index, 1);
      }
      scans = scans;
      if (scans.length == 0) {
        if (page > 1) {
          page--;
        }
        fetchListScans();
      }
    } catch (err) {
      status = "error deleting scan " + scanId;
    }
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
      status = "";
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
      <th>Details</th>
      <th>Scan type</th>
      <th>Start Time</th>
      <th>Duration</th>
      <th>Metadata</th>
      <th>Actions</th>
    </tr>
    {#each scans as scan}
      <tr>
        <td>{scan.scan_id}</td>
        <td>
          <i class="material-icons" on:click={() => loadScanData(scan.scan_id)}>
            forward
          </i>
        </td>
        <td>{scan.ScanType}</td>
        <td>{scan.ScanStartTime}</td>
        {#if scan.ScanEndTime.Valid}
          <td>{scan.Duration}</td>
        {:else}
          <td class="ongoing">{scan.Duration}</td>
        {/if}
        <td>{scan.Metadata}</td>
        <td>
          <i class="material-icons" on:click={() => deleteScan(scan.scan_id)}>
            delete
          </i>
        </td>
      </tr>
    {/each}
  </table>
{/if}

<p>{status}</p>

<style>
  table {
    width: 100%;
    border: 1px solid black;
  }

  td.ongoing {
    background-color: rgb(193, 166, 150);
  }

  th,
  td {
    border: 1px solid black;
  }
</style>
