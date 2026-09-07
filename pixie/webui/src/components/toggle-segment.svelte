<script lang="ts">
interface Props {
	testid: string;
	label: string;
	active: boolean;
	onclick: () => void;
}

let { testid, label, active, onclick }: Props = $props();
</script>

<button
	type="button"
	class="toggle"
	data-testid={testid}
	data-active={active}
	aria-pressed={active}
	{onclick}
>
	{label}
</button>
