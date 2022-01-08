<script lang="ts">
  let selected: any;
  let localPath = "/Users/";
  let filter = "";
  let optons = [
    { id: 0, text: `Select One` },
    { id: 1, text: `Local` },
    { id: 2, text: `Google Drive` },
    { id: 3, text: `Google Storage` },
  ];
  selected = optons[0];
  function submit() {
    alert(`selected option ${selected.id} (${selected.text})`);
  }
</script>

<form>
  <div class="col-25">
    <label for="scanType">Scan Type</label>
  </div>
  <div class="col-75">
    <select id="scanType" bind:value={selected}>
      {#each optons as option}
        <option value={option}>
          {option.text}
        </option>
      {/each}
    </select>
  </div>
  {#if selected.id == 1}
    <div class="col-25">
      <label for="scanType">Local Path</label>
    </div>
    <div class="col-75">
      <input id="path" type="text" bind:value={localPath} />
    </div>
  {/if}

  {#if selected.id == 2 || selected.id == 3}
    <div class="col-25">
      <label for="scanType">File filter</label>
    </div>
    <div class="col-75">
      <input id="filter" type="text" bind:value={filter} />
    </div>
  {/if}

  <input type="button" on:click={submit} value="Submit" />
</form>

<style>
  * {
    box-sizing: border-box;
  }

  input[type="text"],
  select {
    width: 100%;
    padding: 12px;
    border: 1px solid #ccc;
    border-radius: 4px;
    resize: vertical;
  }

  label {
    padding: 12px 12px 12px 0;
    display: inline-block;
  }

  input[type="button"] {
    background-color: #4c4caf;
    color: white;
    padding: 12px 20px;
    border: none;
    border-radius: 4px;
    cursor: pointer;
    float: right;
  }

  input[type="button"]:hover {
    background-color: #4545a0;
  }
  .col-25 {
    float: left;
    width: 25%;
    margin-top: 6px;
  }

  .col-75 {
    float: left;
    width: 75%;
    margin-top: 6px;
  }

  /* Responsive layout - when the screen is less than 600px wide, make the two columns stack on top of each other instead of next to each other */
  @media screen and (max-width: 600px) {
    .col-25,
    .col-75,
    input[type="button"] {
      width: 100%;
      margin-top: 0;
    }
  }
</style>
