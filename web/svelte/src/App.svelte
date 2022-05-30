<script lang="ts">
	import Header from "./Header.svelte";
	import InitiateScans from "./InitiateScans.svelte";
	import ListScans from "./ListScans.svelte";
	import ListScanData from "./ListScanData.svelte";
	import MessageMetaData from "./MessageMetaData.svelte";
	import Menu from "./Menu.svelte";
	import NotFound from "./NotFound.svelte";
	import Photos from "./Photos.svelte";
	import { Router, Route } from "svelte-navigator";

	let heading = "Backup Manager";
</script>

<Router>
	<header>
		<Header {heading} />
		<Menu />
	</header>
	<main>
		<Route path="/" component={ListScans} />
		<Route path="startScan" component={InitiateScans} />
		<Route path="scan/photos/:scanId" component={Photos} />
		<Route path="scan/gmail/:scanId" component={MessageMetaData} />
		<Route path="scan/:scanType/:scanId" component={ListScanData} />
		<Route component={NotFound} />
	</main>
</Router>

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
