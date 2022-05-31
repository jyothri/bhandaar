<script lang="ts">
  import { token } from "./stores";
  export let selected: string;

  const urlParams = new URLSearchParams(window.location.search);
  if (urlParams.has("refresh_token")) {
    token.set(urlParams.get("refresh_token"));
  }
  let linked: boolean = $token != "";

  function unlinkAccount() {
    token.set("");
    linked = false;
    window.location.href = `${window.location.origin}/startScan`;
  }

  function linkAccount() {
    const spiUrl = "https://accounts.google.com/o/oauth2/v2/auth";
    const driveScope = "https://www.googleapis.com/auth/drive.readonly";
    const gmailScope = "https://www.googleapis.com/auth/gmail.readonly";
    const photosScope =
      "https://www.googleapis.com/auth/photoslibrary.readonly";
    const sharedPhotosScope =
      "https://www.googleapis.com/auth/photoslibrary.sharing";
    const scope = `${driveScope} ${gmailScope} ${photosScope} ${sharedPhotosScope}`;
    const clientId =
      "112106509963-uluv01bacctqgd7mr003u7r1lpq3899n.apps.googleusercontent.com";
    const state = "YOUR_CUSTOM_STATE";
    const redirectUri = `${window.location.origin}/oauth/glink`;
    const addtionalParams = "&access_type=offline&prompt=consent";
    const url = `${spiUrl}?response_type=code&scope=${scope}&client_id=${clientId}&state=${state}&redirect_uri=${redirectUri}${addtionalParams}`;
    window.location.href = url;
  }
</script>

{#if selected == "GDrive" || selected == "GMail" || selected == "GPhotos"}
  <input
    type="button"
    on:click={linkAccount}
    value="Link Google Account"
    disabled={linked}
  />
  <input
    type="button"
    on:click={unlinkAccount}
    value="Unlink Google Account"
    disabled={!linked}
  />
{/if}

<style>
  input[type="button"] {
    background-color: #cc4caf;
    color: white;
    padding: 12px 20px;
    border: none;
    border-radius: 4px;
    cursor: pointer;
    float: right;
    width: 40%;
    margin: 0 1em 1em 1em;
  }

  input[type="button"]:hover {
    background-color: #b636b0;
  }

  input[type="button"]:disabled {
    background-color: #acacbb;
  }
</style>
