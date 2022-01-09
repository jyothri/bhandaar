<script lang="ts">
  let selected: any;
  let readyToSubmit = false;
  let localPath = "/Users/jyothri/test";
  let filter = "name contains 'tesla'";
  let bucket = "jyo-pics";
  let submittedScans: number[] = [];
  let options = [
    { id: 0, text: `Select One` },
    { id: 1, text: `Local` },
    { id: 2, text: `Google Drive` },
    { id: 3, text: `Google Storage` },
  ];
  selected = options[0];
  const apiEndpoint = "http://localhost:8090";

  async function submit() {
    const res = await fetch(`${apiEndpoint}/api/scans`, {
      method: "POST",
      body: JSON.stringify({
        scan_type: selected.text,
        localPath,
        filter,
        gs_bucket: bucket,
      }),
    });
    const json = await res.json();
    submittedScans.push(json.scan_id);
    submittedScans = submittedScans; // needed for svelte to react.
  }

  function validate() {
    switch (selected.id) {
      case 0:
      default:
        readyToSubmit = false;
        return;
      case 1:
        if (localPath == "") {
          readyToSubmit = false;
          return;
        }
        readyToSubmit = true;
        return;
      case 2:
      case 3:
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
          {#each options as option}
            <option value={option}>
              {option.text}
            </option>
          {/each}
        </select>
      </div>
    </div>
    {#if selected.id == 1}
      <div class="row">
        <div class="column">
          <label for="scanType">Local Path</label>
        </div>
        <div class="column">
          <input id="path" type="text" bind:value={localPath} />
        </div>
      </div>
    {/if}

    {#if selected.id == 2}
      <div class="row">
        <div class="column">
          <label for="scanType">File filter</label>
        </div>
        <div class="column">
          <input id="filter" type="text" bind:value={filter} />
        </div>
      </div>
    {/if}
    {#if selected.id == 3}
      <div class="row">
        <div class="column">
          <label for="scanType">Google Storage Bucket</label>
        </div>
        <div class="column">
          <input id="filter" type="text" bind:value={bucket} />
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

  /* Responsive layout - when the screen is less than 600px wide, make the two columns stack on top of each other instead of next to each other */
  @media screen and (max-width: 600px) {
    input[type="button"] {
      width: 100%;
      margin-top: 0;
    }
  }
</style>
