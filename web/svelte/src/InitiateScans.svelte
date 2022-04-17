<script lang="ts">
  let selected: string;
  let readyToSubmit = false;
  let submittedScans: number[] = [];
  let albums: Album[] = [];
  let status: string = "";
  selected = "None";
  const apiEndpoint = "http://localhost:8090";

  interface LocalScan {
    Path: string;
  }

  interface GDriveScan {
    QueryString: string;
  }

  interface GStorageScan {
    Bucket: string;
  }

  interface GMailScan {
    Filter: string;
  }

  interface GPhotosScan {
    AlbumId: string;
    FetchSize: boolean;
    FetchMd5Hash: boolean;
  }

  enum ScanType {
    Local = "Local",
    GDrive = "GDrive",
    GStorage = "GStorage",
    GMail = "GMail",
    GPhotos = "GPhotos",
  }

  interface ScanMetadata {
    ScanType?: ScanType;
    LocalScan: LocalScan;
    GDriveScan: GDriveScan;
    GStorageScan: GStorageScan;
    GMailScan: GMailScan;
    GPhotosScan: GPhotosScan;
  }

  interface Album {
    Id: string;
    Title: string;
    ProductUrl: string;
    MediaItemsCount: string;
    CoverPhotoBaseUrl: string;
    CoverPhotoMediaItemId: string;
  }

  let scanMetadata: ScanMetadata = {
    LocalScan: { Path: "" },
    GDriveScan: { QueryString: "" },
    GStorageScan: { Bucket: "" },
    GMailScan: { Filter: "" },
    GPhotosScan: {
      AlbumId: "",
      FetchSize: false,
      FetchMd5Hash: false,
    },
  };

  async function submit() {
    const res = await fetch(`${apiEndpoint}/api/scans`, {
      method: "POST",
      body: JSON.stringify(scanMetadata),
    });
    const json = await res.json();
    submittedScans.push(json.scan_id);
    submittedScans = submittedScans; // needed for svelte to react.
  }

  let fetchListAlbums = async () => {
    try {
      status = "fetching albums";
      const res = await fetch(`${apiEndpoint}/api/photos/albums`);
      let response = await res.json();
      let albumSize = response.pagination_info.size;
      if (albumSize == 0) {
        status = "no albums";
        return;
      }
      albums = response.albums;
      status = "";
    } catch (err) {
      status = "error getting albums";
    }
  };

  function validate() {
    switch (selected) {
      case "None":
      default:
        readyToSubmit = false;
        return;
      case "Local":
        scanMetadata.ScanType = ScanType.Local;
        if (scanMetadata.LocalScan.Path == "") {
          readyToSubmit = false;
          return;
        }
        readyToSubmit = true;
        return;
      case "GDrive":
        scanMetadata.ScanType = ScanType.GDrive;
        readyToSubmit = true;
        return;
      case "GStorage":
        scanMetadata.ScanType = ScanType.GStorage;
        readyToSubmit = true;
        return;
      case "GMail":
        scanMetadata.ScanType = ScanType.GMail;
        readyToSubmit = true;
        return;
      case "GPhotos":
        if (albums.length == 0) {
          fetchListAlbums();
        }
        scanMetadata.ScanType = ScanType.GPhotos;
        readyToSubmit = true;
        return;
    }
  }
</script>

<div class="container">
  <form>
    <div class="row">
      <div class="column">
        <label for="scanType">Scan Type</label>
      </div>
      <div class="column">
        <select id="scanType" bind:value={selected} on:change={validate}>
          <option value="None"> Select One </option>
          {#each Object.keys(ScanType) as scanType}
            <option value={scanType}>
              {scanType}
            </option>
          {/each}
        </select>
      </div>
    </div>

    {#if selected == "Local"}
      <div class="row">
        <div class="column">
          <label for="scanType">Local Path</label>
        </div>
        <div class="column">
          <input
            id="path"
            type="text"
            on:change={validate}
            bind:value={scanMetadata.LocalScan.Path}
          />
        </div>
      </div>
    {/if}
    {#if selected == "GDrive"}
      <div class="row">
        <div class="column">
          <label for="scanType">File filter</label>
        </div>
        <div class="column">
          <input
            id="filter"
            type="text"
            on:change={validate}
            bind:value={scanMetadata.GDriveScan.QueryString}
          />
        </div>
      </div>
    {/if}
    {#if selected == "GStorage"}
      <div class="row">
        <div class="column">
          <label for="scanType">Google Storage Bucket</label>
        </div>
        <div class="column">
          <input
            id="filter"
            type="text"
            on:change={validate}
            bind:value={scanMetadata.GStorageScan.Bucket}
          />
        </div>
      </div>
    {/if}

    {#if selected == "GMail"}
      <div class="row">
        <div class="column">
          <label for="scanType">Query filter</label>
        </div>
        <div class="column">
          <input
            id="filter"
            type="text"
            on:change={validate}
            bind:value={scanMetadata.GMailScan.Filter}
          />
        </div>
      </div>
    {/if}

    {#if selected == "GPhotos"}
      <div class="row">
        <div class="column">
          <label for="scanType">Albums selection</label>
        </div>
        <div class="column">
          <select
            id="scanType"
            bind:value={scanMetadata.GPhotosScan.AlbumId}
            on:change={validate}
          >
            <option value=""> All Albums </option>
            {#each albums as album}
              <option value={album.Id}>
                {album.Title}
              </option>
            {/each}
          </select>
        </div>
      </div>
      <div class="row">
        <div class="column">
          <label for="scanType">Accurate Size:</label>
        </div>
        <div class="column">
          <label>
            <input
              type="radio"
              bind:group={scanMetadata.GPhotosScan.FetchSize}
              name="fetchSize"
              value={false}
              on:change={validate}
            />
            No
          </label>
          <label>
            <input
              type="radio"
              bind:group={scanMetadata.GPhotosScan.FetchSize}
              name="fetchSize"
              value={true}
              on:change={validate}
            />
            Yes
          </label>
        </div>
      </div>
    {/if}

    <div class="row">
      <div class="column center">
        <input
          type="button"
          on:click={submit}
          value="Submit"
          disabled={!readyToSubmit}
        />
      </div>
    </div>
  </form>

  <p>{status}</p>

  {#each submittedScans as submittedScanId}
    <div class="row">
      Submmited. scan_id: {submittedScanId}
    </div>
  {/each}
</div>

<style>
  .container {
    display: flex; /* or inline-flex */
    flex-direction: column;
  }

  .row {
    display: flex;
    flex-direction: row;
    width: 100%;
  }

  .column {
    display: flex;
    flex-basis: 100%;
    flex: 1;
  }

  .center {
    justify-content: center;
  }

  input[type="text"],
  select {
    width: 100%;
  }

  input[type="button"] {
    background-color: #4c4caf;
    color: white;
    padding: 12px 20px;
    border: none;
    border-radius: 4px;
    cursor: pointer;
    float: right;
    width: 25%;
  }

  input[type="button"]:hover {
    background-color: #4545a0;
  }

  input[type="button"]:disabled {
    background-color: #acacbb;
  }

  label {
    padding-right: 1.5em;
  }

  /* Responsive layout - when the screen is less than 600px wide, make the two columns stack on top of each other instead of next to each other */
  @media screen and (max-width: 600px) {
    input[type="button"] {
      width: 100%;
      margin-top: 0;
    }
  }
</style>
