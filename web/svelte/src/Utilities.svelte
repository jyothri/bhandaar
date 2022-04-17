<script lang="ts">
  export const getSize = (bytes: number) => {
    if (bytes < 0) {
      return "N/A";
    }
    if (bytes < 1024) {
      return Math.round(bytes) + " B";
    }
    bytes = bytes / 1024;
    if (bytes < 1024) {
      return Math.round(bytes) + " KB";
    }
    bytes = bytes / 1024;
    if (bytes < 1024) {
      return Math.round(bytes) + " MB";
    }
    bytes = bytes / 1024;
    if (bytes < 1024) {
      return Math.round(bytes) + " GB";
    }
  };
</script>
