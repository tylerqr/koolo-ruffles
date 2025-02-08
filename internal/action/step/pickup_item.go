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
)

const (
	maxInteractions = 9 // 10 attempts since we start at 0
	spiralDelay     = 50 * time.Millisecond
	clickDelay      = 25 * time.Millisecond
	pickupTimeout   = 3 * time.Second
)

var (
	ErrItemTooFar        = errors.New("item is too far away")
	ErrNoLOSToItem       = errors.New("no line of sight to item")
	ErrMonsterAroundItem = errors.New("monsters detected around item")
	ErrCastingMoving     = errors.New("char casting or moving")
)

func PickupItem(it data.Item, itemPickupAttempt int) error {
	ctx := context.Get()
	ctx.SetLastStep("PickupItem")

	// Casting skill/moving return back
	for ctx.Data.PlayerUnit.Mode == mode.CastingSkill || ctx.Data.PlayerUnit.Mode == mode.Running || ctx.Data.PlayerUnit.Mode == mode.Walking || ctx.Data.PlayerUnit.Mode == mode.WalkingInTown {
		time.Sleep(25 * time.Millisecond)
		return ErrCastingMoving
	}

	// Calculate base screen position for item
	baseX := it.Position.X - 1
	baseY := it.Position.Y - 1
	baseScreenX, baseScreenY := ctx.PathFinder.GameCoordsToScreenCords(baseX, baseY)

	// Check for monsters first
	if hasHostileMonstersNearby(it.Position) {
		return ErrMonsterAroundItem
	}

	// Validate line of sight
	if !ctx.PathFinder.LineOfSight(ctx.Data.PlayerUnit.Position, it.Position) {
		return ErrNoLOSToItem
	}

	// Check distance
	distance := ctx.PathFinder.DistanceFromMe(it.Position)
	if distance >= 7 {
		// Try to move closer if possible
		if distance < 10 {
			err := MoveTo(it.Position)
			if err == nil {
				// Recalculate distance after moving
				distance = ctx.PathFinder.DistanceFromMe(it.Position)
			}
		}
		
		if distance >= 7 {
			return fmt.Errorf("%w (%d): %s", ErrItemTooFar, distance, it.Desc().Name)
		}
	}

	ctx.Logger.Debug(fmt.Sprintf("Picking up: %s [%s]", it.Desc().Name, it.Quality.ToString()))

	// Track interaction state
	waitingForInteraction := time.Time{}
	spiralAttempt := 0
	targetItem := it
	lastMonsterCheck := time.Now()
	const monsterCheckInterval = 150 * time.Millisecond

	startTime := time.Now()

	for {
		ctx.PauseIfNotPriority()
		ctx.RefreshGameData()

		// Periodic monster check
		if time.Since(lastMonsterCheck) > monsterCheckInterval {
			if hasHostileMonstersNearby(it.Position) {
				return ErrMonsterAroundItem
			}
			lastMonsterCheck = time.Now()
		}

		// Check if item still exists
		currentItem, exists := findItemOnGround(targetItem.UnitID)
		if !exists {
			ctx.Logger.Info(fmt.Sprintf("Picked up: %s [%s] | Attempt:%d | Spiral:%d", 
				targetItem.Desc().Name, targetItem.Quality.ToString(), 
				itemPickupAttempt, spiralAttempt))
			return nil // Success!
		}

		// Check timeout conditions
		if spiralAttempt > maxInteractions ||
			(!waitingForInteraction.IsZero() && time.Since(waitingForInteraction) > pickupTimeout) ||
			time.Since(startTime) > pickupTimeout {
			return fmt.Errorf("failed to pick up %s after %d attempts", it.Desc().Name, spiralAttempt)
		}

		// Get spiral offset with slightly larger pattern for better coverage
		offsetX, offsetY := getSpiralOffset(spiralAttempt)
		cursorX := baseScreenX + offsetX
		cursorY := baseScreenY + offsetY

		// Move cursor and verify position
		ctx.HID.MovePointer(cursorX, cursorY)
		ctx.RefreshGameData()
		time.Sleep(50 * time.Millisecond)

		// If item is hovered, try multiple quick clicks
		if currentItem.IsHovered {
			// Try up to 3 quick clicks
			for clickAttempt := 0; clickAttempt < 3; clickAttempt++ {
				ctx.HID.Click(game.LeftButton, cursorX, cursorY)
				time.Sleep(50 * time.Millisecond)
				
				// Verify if item still exists after click
				if _, stillExists := findItemOnGround(targetItem.UnitID); !stillExists {
					return nil // Successfully picked up
				}
			}

			if waitingForInteraction.IsZero() {
				waitingForInteraction = time.Now()
			}
		}

		if spiralAttempt > 0 && spiralAttempt%15 == 0 && resetAttempts < maxResetAttempts {
			// Reset position and start spiral pattern over
			ctx.HID.MovePointer(baseScreenX, baseScreenY)
			time.Sleep(100 * time.Millisecond)
			spiralAttempt = 0
			resetAttempts++
			ctx.Logger.Debug("Resetting cursor position for pickup attempt",
				slog.String("item", it.Desc().Name),
				slog.Int("resetAttempt", resetAttempts))
		}

		spiralAttempt++
	}
}

// Helper function for improved spiral pattern
func getSpiralOffset(attempt int) (int, int) {
	// Increase spiral size slightly for better coverage
	spiralScale := 1.2
	baseOffset := utils.ItemSpiral(attempt)
	return int(float64(baseOffset.X) * spiralScale), 
		   int(float64(baseOffset.Y) * spiralScale)
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
