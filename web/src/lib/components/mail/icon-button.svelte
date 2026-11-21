<script lang="ts">
	import { mergeProps } from 'bits-ui';
	import { Button, type ButtonProps } from '#lib/components/ui/button/index.js';
	import * as Tooltip from '#lib/components/ui/tooltip/index.js';

	type Side = 'top' | 'right' | 'bottom' | 'left';

	// An icon-only button (or link, with href) whose label is both its accessible
	// name and its tooltip.
	let {
		label,
		side = 'bottom',
		variant = 'ghost',
		size = 'icon',
		children,
		...rest
	}: ButtonProps & { label: string; side?: Side } = $props();
</script>

<Tooltip.Root>
	<Tooltip.Trigger>
		{#snippet child({ props })}
			<Button {...mergeProps(rest, props)} {variant} {size} aria-label={label}>
				{@render children?.()}
			</Button>
		{/snippet}
	</Tooltip.Trigger>
	<Tooltip.Content {side}>{label}</Tooltip.Content>
</Tooltip.Root>
