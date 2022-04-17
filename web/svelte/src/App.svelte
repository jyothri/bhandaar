<script lang="ts">
	import Header from "./Header.svelte";
	import InitiateScans from "./InitiateScans.svelte";
	import ListScans from "./ListScans.svelte";
	import ListScanData from "./ListScanData.svelte";
	import MessageMetaData from "./MessageMetaData.svelte";
	import Menu from "./Menu.svelte";
	import NotFound from "./NotFound.svelte";
	import Photos from "./Photos.svelte";
	import Router from "svelte-spa-router";

	let heading = "Backup Manager";

	const routes = {
		"/": ListScans,
		"/scan/photos/:scanId": Photos,
		"/scan/gmail/:scanId": MessageMetaData,
		"/scan/:scanType/:scanId": ListScanData,
		"/startScan": InitiateScans,

		// Catch-all
		// This is optional, but if present it must be the last
		"*": NotFound,
	};
</script>

<main>
	<Header {heading} />
	<Menu />
	<Router {routes} />
</main>

<style>
	main {
		text-align: center;
		padding: 1em;
		max-width: 240px;
		margin: 0 auto;
	}

	@media (min-width: 640px) {
		main {
			max-width: none;
		}
	}
</style>
