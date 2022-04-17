<script lang="ts">
  import { onMount } from "svelte";
  import Pagination from "./Pagination.svelte";
  import Utilities from "./Utilities.svelte";

  export let scanId: number;
  export let params: { [key: string]: string } = {};
  let utilities: any;

  class OptionalString {
    String: string;
    Valid: boolean;
  }

  class OptionalInt64 {
    Int64: number;
    Valid: boolean;
  }

  class MessageMetadata {
    message_metadata_id: number;
    ScanId: number;
    MessageId: OptionalString;
    ThreadId: OptionalString;
    LabelIds: OptionalString;
    From: OptionalString;
    To: OptionalString;
    Subject: OptionalString;
    Date: OptionalString;
    SizeEstimate: OptionalInt64;
  }

  const pageSize = 10;
  const apiEndpoint = "http://localhost:8090";
  let messageMetadata: MessageMetadata[] = [];
  let totalScans = 0;
  let page = 1;
  let status = "";
  let maxPages = 0;
  let prevScanId = 0;
  let prevPage = 0;

  let fetchListMessageMetaData = async () => {
    if (scanId == prevScanId && prevPage == page) {
      return;
    }
    prevScanId = scanId;
    prevPage = page;
    status = "loading...";
    try {
      const res = await fetch(
        `${apiEndpoint}/api/gmaildata/${scanId}?page=${page}`
      );
      let response = await res.json();
      totalScans = response.pagination_info.size;
      if (totalScans == 0) {
        messageMetadata = [];
        status = "no data";
        return;
      }
      messageMetadata = response.message_metadata;
      maxPages = 1 + Math.trunc(totalScans / pageSize);
      if (totalScans % pageSize == 0) {
        maxPages--;
      }
    } catch (err) {
      messageMetadata = [];
      status = "error getting data";
    }
  };

  let loadPreviousPage = async () => {
    page--;
    await fetchListMessageMetaData();
  };

  let loadNextPage = async () => {
    page++;
    await fetchListMessageMetaData();
  };

  onMount(async () => {
    scanId = parseInt(params.scanId);
    if (scanId > 0) {
      await fetchListMessageMetaData();
    }
  });
</script>

<Utilities bind:this={utilities} />

<h1>GMail data for scan: {scanId}</h1>
{#if messageMetadata.length > 0}
  <Pagination
    {page}
    {maxPages}
    on:loadNextPage={loadNextPage}
    on:loadPreviousPage={loadPreviousPage}
  />
  <table>
    <tr>
      <th>id</th>
      <th>From</th>
      <th>To</th>
      <th>Subject</th>
      <th>Size</th>
      <th>Labels</th>
      <th>Date</th>
    </tr>
    {#each messageMetadata as messageMetadatum}
      <tr>
        <td>{messageMetadatum.message_metadata_id}</td>
        <td>{messageMetadatum.From.String}</td>
        <td>{messageMetadatum.To.String}</td>
        <td>{messageMetadatum.Subject.String}</td>
        <td>{@html utilities.getSize(messageMetadatum.SizeEstimate.Int64)}</td>
        <td>{messageMetadatum.LabelIds.String}</td>
        <td>{messageMetadatum.Date.String}</td>
      </tr>
    {/each}
  </table>
{:else}
  <!-- this block renders when there is no data -->
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
