<script lang="ts">
  import { token } from "./stores";

  const urlParams = new URLSearchParams(window.location.search);

  if (urlParams.has("refresh_token")) {
    token.set(urlParams.get("refresh_token"));
  }

  async function linkAccount() {
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

<input
  type="button"
  class="secondaycta"
  on:click={linkAccount}
  value="Link Google Account"
/>

<style>
  .secondaycta {
    background-color: #cc4caf;
    width: 40%;
  }
</style>
