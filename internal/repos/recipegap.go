package repos

import "github.com/pedropinheiro75/stackfork/internal/inventory"

// The gap rule lives in inventory (which also reports it as an instance
// warning; repos imports inventory, not the other way round).

// Gap is inventory.Gap.
type Gap = inventory.Gap

// RecipeGap is inventory.RecipeGap: the one rule behind every gap warning.
func RecipeGap(main, root string, files []string, insts []inventory.Instance) Gap {
	return inventory.RecipeGap(main, root, files, insts)
}
