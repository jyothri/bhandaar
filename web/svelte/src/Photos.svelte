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

  interface OptionalTime {
    Time: string;
    Valid: boolean;
  }

  class PhotosMediaItem {
    photos_media_item_id: number;
    ScanId: number;
    media_item_id: string;
    ProductUrl: string;
    MimeType: OptionalString;
    Filename: string;
    Size: OptionalInt64;
    ModifiedTime: OptionalTime;
    Md5hash: OptionalString;
    ContributorDisplayName: OptionalString;
  }

  const pageSize = 10;
  const apiEndpoint = "http://localhost:8090";
  let photosMediaItems: PhotosMediaItem[] = [];
  let totalScans = 0;
  let page = 1;
  let status = "";
  let maxPages = 0;
  let prevScanId = 0;
  let prevPage = 0;

  let fetchListPhotosData = async () => {
    if (scanId == prevScanId && prevPage == page) {
      return;
    }
    prevScanId = scanId;
    prevPage = page;
    status = "loading...";
    try {
      const res = await fetch(
        `${apiEndpoint}/api/photos/${scanId}?page=${page}`
      );
      let response = await res.json();
      totalScans = response.pagination_info.size;
      if (totalScans == 0) {
        photosMediaItems = [];
        status = "no data";
        return;
      }
      photosMediaItems = response.photos_media_item;
      maxPages = 1 + Math.trunc(totalScans / pageSize);
      if (totalScans % pageSize == 0) {
        maxPages--;
      }
    } catch (err) {
      photosMediaItems = [];
      status = "error getting data";
    }
  };

  let loadPreviousPage = async () => {
    page--;
    await fetchListPhotosData();
  };

  let loadNextPage = async () => {
    page++;
    await fetchListPhotosData();
  };

  onMount(async () => {
    scanId = parseInt(params.scanId);
    if (scanId > 0) {
      await fetchListPhotosData();
    }
  });
</script>

<Utilities bind:this={utilities} />

<h1>Photos data for scan: {scanId}</h1>

{#if photosMediaItems.length > 0}
  <Pagination
    {page}
    {maxPages}
    on:loadNextPage={loadNextPage}
    on:loadPreviousPage={loadPreviousPage}
  />
  <table>
    <tr>
      <th class="id">id</th>
      <th class="productUrl">Product url</th>
      <th class="otherCols">Mime type</th>
      <th class="otherCols">Filename</th>
      <th class="otherCols">Size</th>
      <th class="otherCols">File Modified time</th>
      <th class="otherCols">Md5Hash</th>
      <th class="otherCols">Contributor</th>
    </tr>
    {#each photosMediaItems as photosMediaItem}
      <tr>
        <td>{photosMediaItem.media_item_id}</td>
        <td class="productUrl">
          <a href={photosMediaItem.ProductUrl}>{photosMediaItem.ProductUrl}</a>
        </td>
        <td>{photosMediaItem.MimeType.String}</td>
        <td>{photosMediaItem.Filename}</td>
        <td>{@html utilities.getSize(photosMediaItem.Size.Int64)}</td>
        <td>{photosMediaItem.ModifiedTime.Time}</td>
        <td>{photosMediaItem.Md5hash.String}</td>
        <td>{photosMediaItem.ContributorDisplayName.String}</td>
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
    table-layout: fixed;
  }

  th,
  td {
    border: 1px solid black;
    width: 20 em;
    overflow: scroll;
  }

  th.id {
    width: 20%;
  }

  td.productUrl {
    width: 20%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
