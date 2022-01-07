<script lang="ts">
	import Header from "./Header.svelte";
	import Actions from "./Actions.svelte";
	let heading = "Backup Manager";
</script>

<main>
	<Header {heading} />
	<Actions />
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
