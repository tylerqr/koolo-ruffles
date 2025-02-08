package step

import (
	"errors"
	"fmt"
	"time"

	"github.com/hectorgimenez/d2go/pkg/data"
	"github.com/hectorgimenez/d2go/pkg/data/item"
	"github.com/hectorgimenez/d2go/pkg/data/mode"
	"github.com/hectorgimenez/d2go/pkg/data/stat"
	"github.com/hectorgimenez/koolo/internal/context"
	"github.com/hectorgimenez/koolo/internal/game"
	"github.com/hectorgimenez/koolo/internal/pather"
	"github.com/hectorgimenez/koolo/internal/utils"
	"github.com/hectorgimenez/koolo/internal/log"
)

const (
	maxInteractions = 30
	spiralDelay     = 50 * time.Millisecond
	clickDelay      = 100 * time.Millisecond
	pickupTimeout   = 8 * time.Second
)

var (
	ErrItemTooFar        = errors.New("item is too far away")
	ErrNoLOSToItem       = errors.New("no line of sight to item")
	ErrMonsterAroundItem = errors.New("monsters detected around item")
	ErrCastingMoving     = errors.New("char casting or moving")
)

func PickupItem(it data.Item) error {
	ctx := context.Get()
	startTime := time.Now()
	waitingForInteraction := time.Zero
	spiralAttempt := 0
	lastMonsterCheck := time.Now()
	const monsterCheckInterval = time.Second

	// Initial position check
	initialPlayerPos := ctx.Data.PlayerUnit.Position
	baseX, baseY := it.Position.X, it.Position.Y
	baseScreenX, baseScreenY := ctx.PathFinder.GameCoordsToScreenCords(baseX, baseY)

	for {
		ctx.PauseIfNotPriority()
		ctx.RefreshGameData()

		// 1. Verify player hasn't moved
		if !initialPlayerPos.Equal(ctx.Data.PlayerUnit.Position) {
			ctx.Logger.Debug("Player position changed during pickup attempt, recalculating",
				log.String("item", it.Desc().Name))
			initialPlayerPos = ctx.Data.PlayerUnit.Position
			baseScreenX, baseScreenY = ctx.PathFinder.GameCoordsToScreenCords(baseX, baseY)
		}

		// 2. Verify item still exists and hasn't moved
		currentItem, exists := findItemOnGround(it.UnitID)
		if !exists {
			ctx.Logger.Info(fmt.Sprintf("Picked up: %s [%s] | Attempt:%d",
				it.Desc().Name, it.Quality.ToString(), spiralAttempt))
			return nil
		}

		// 3. Verify item position hasn't changed
		if currentItem.Position.X != baseX || currentItem.Position.Y != baseY {
			ctx.Logger.Debug("Item position changed, updating coordinates",
				log.String("item", it.Desc().Name))
			baseX, baseY = currentItem.Position.X, currentItem.Position.Y
			baseScreenX, baseScreenY = ctx.PathFinder.GameCoordsToScreenCords(baseX, baseY)
		}

		// Check timeout conditions
		if spiralAttempt > maxInteractions ||
			(!waitingForInteraction.IsZero() && time.Since(waitingForInteraction) > pickupTimeout) ||
			time.Since(startTime) > pickupTimeout {
			return fmt.Errorf("failed to pick up %s after %d attempts", it.Desc().Name, spiralAttempt)
		}

		// 4. Monster check with increased frequency for valuable items
		if time.Since(lastMonsterCheck) > monsterCheckInterval {
			if hasHostileMonstersNearby(currentItem.Position) {
				return ErrMonsterAroundItem
			}
			lastMonsterCheck = time.Now()
		}

		// 5. Calculate and verify cursor position
		offsetX, offsetY := utils.ItemSpiral(spiralAttempt)
		targetCursorX := baseScreenX + offsetX
		targetCursorY := baseScreenY + offsetY

		// Move cursor and verify position
		ctx.HID.MovePointer(targetCursorX, targetCursorY)
		time.Sleep(50 * time.Millisecond)
		
		// 6. Verify cursor position after movement
		actualX, actualY := ctx.HID.GetCursorPosition()
		if abs(actualX-targetCursorX) > 5 || abs(actualY-targetCursorY) > 5 {
			ctx.Logger.Debug("Cursor position mismatch, retrying movement",
				log.Int("targetX", targetCursorX),
				log.Int("actualX", actualX),
				log.Int("targetY", targetCursorY),
				log.Int("actualY", actualY))
			continue
		}

		ctx.RefreshGameData()

		// 7. Verify item is actually hovered
		if currentItem.IsHovered {
			// Try multiple quick clicks when item is hovered
			for clickAttempt := 0; clickAttempt < 3; clickAttempt++ {
				// Final position verification before click
				if !verifyPickupConditions(ctx, currentItem, initialPlayerPos) {
					break
				}
				
				ctx.HID.Click(game.LeftButton, targetCursorX, targetCursorY)
				time.Sleep(clickDelay)

				// Check if item was picked up
				if _, stillExists := findItemOnGround(it.UnitID); !stillExists {
					return nil
				}
			}

			if waitingForInteraction.IsZero() {
				waitingForInteraction = time.Now()
			}
		}

		spiralAttempt++
	}
}

// Helper function to verify all conditions are still valid before clicking
func verifyPickupConditions(ctx *context.Context, item data.Item, initialPos data.Position) bool {
	// Verify player hasn't moved
	if !initialPos.Equal(ctx.Data.PlayerUnit.Position) {
		return false
	}

	// Verify item still exists and is still hovered
	currentItem, exists := findItemOnGround(item.UnitID)
	if !exists || !currentItem.IsHovered {
		return false
	}

	// Verify item position hasn't changed
	if !currentItem.Position.Equal(item.Position) {
		return false
	}

	return true
}

// Helper function to calculate absolute difference
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func hasHostileMonstersNearby(pos data.Position) bool {
	ctx := context.Get()

	for _, monster := range ctx.Data.Monsters.Enemies() {
		if monster.Stats[stat.Life] > 0 && pather.DistanceFromPoint(pos, monster.Position) <= 4 {
			return true
		}
	}
	return false
}

func findItemOnGround(targetID data.UnitID) (data.Item, bool) {
	ctx := context.Get()

	for _, i := range ctx.Data.Inventory.ByLocation(item.LocationGround) {
		if i.UnitID == targetID {
			return i, true
		}
	}
	return data.Item{}, false
}

func isChestHovered() bool {
	ctx := context.Get()

	for _, o := range ctx.Data.Objects {
		if o.IsChest() && o.IsHovered {
			return true
		}
	}
	return false
}
