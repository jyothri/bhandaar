<script lang="ts">
  import { afterUpdate } from "svelte";
  import Utilities from "./Utilities.svelte";
  import Pagination from "./Pagination.svelte";

  export let scanId: number;
  export let scanType: string;
  let utilities;

  interface OptionalTime {
    Time: string;
    Valid: boolean;
  }

  interface OptionalString {
    String: string;
    Valid: boolean;
  }

  interface OptionalInt64 {
    Int64: number;
    Valid: boolean;
  }

  interface OptionalInt32 {
    Int64: number;
    Valid: boolean;
  }

  interface OptionalBool {
    Bool: boolean;
    Valid: boolean;
  }

  interface ScanData {
    scan_data_id: number;
    Name: OptionalString;
    Path: OptionalString;
    Size: OptionalInt64;
    ModifiedTime: OptionalTime;
    Md5Hash: OptionalString;
    IsDir: OptionalBool;
    FileCount: OptionalInt32;
    ScanId: number;
  }

  const pageSize = 10;
  const apiEndpoint = "http://localhost:8090";
  let scandata: ScanData[] = [];
  let totalScans = 0;
  let page = 1;
  let status = "";
  let maxPages = 0;
  let prevScanId = 0;
  let prevPage = 0;

  let fetchListScanData = async () => {
    if (scanId == prevScanId && prevPage == page) {
      return;
    }
    if (scanType == "gmail" || scanType == "photos") {
      return;
    }
    prevScanId = scanId;
    prevPage = page;
    status = "loading...";
    try {
      const res = await fetch(
        `${apiEndpoint}/api/scans/${scanId}?page=${page}`
      );
      let response = await res.json();
      totalScans = response.pagination_info.size;
      if (totalScans == 0) {
        scandata = [];
        status = "no data";
        return;
      }
      scandata = response.scan_data;
      maxPages = 1 + Math.trunc(totalScans / pageSize);
      if (totalScans % pageSize == 0) {
        maxPages--;
      }
    } catch (err) {
      scandata = [];
      status = "error getting data";
    }
  };

  let loadPreviousPage = async () => {
    page--;
    await fetchListScanData();
  };

  let loadNextPage = async () => {
    page++;
    await fetchListScanData();
  };

  afterUpdate(() => {
    if (scanId > 0) {
      fetchListScanData();
    }
  });
</script>

<Utilities bind:this={utilities} />

{#if scandata.length > 0}
  <Pagination
    {page}
    {maxPages}
    on:loadNextPage={loadNextPage}
    on:loadPreviousPage={loadPreviousPage}
  />
  <table>
    <tr>
      <th>id</th>
      <th>Name</th>
      <th>Path</th>
      <th>Size</th>
      <th>Modified time</th>
      <th>Md5 Hash</th>
    </tr>
    {#each scandata as scandatum}
      <tr>
        <td>{scandatum.scan_data_id}</td>
        <td>{scandatum.Name.String}</td>
        <td>{scandatum.Path.String}</td>
        <td>{@html utilities.getSize(scandatum.Size.Int64)}</td>
        <td>{scandatum.ModifiedTime.Time}</td>
        <td>{scandatum.Md5Hash.String}</td>
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
