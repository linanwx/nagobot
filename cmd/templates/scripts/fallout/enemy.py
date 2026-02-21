"""Enemy tracking: add, hurt, attack, list, clear."""

import re
from .util import (
    error, ok, output, parse_int,
    require_state, require_player, require_enemy,
    roll_dice, save_state,
)


def cmd_enemy_add(args):
    """Add an enemy to the battlefield.
    Usage: enemy-add <name> <hp> <damage_dice> <attack_skill> <drops> [special]
    Example: enemy-add "Raider 1" 25 3d6 10 common "Negotiable"
    """
    if len(args) < 5:
        return error(
            "Usage: enemy-add <name> <hp> <damage_dice> <attack_skill> <drops> [special]",
            hint='Example: enemy-add "Raider 1" 25 3d6 10 common',
        )

    state = require_state()
    if not state:
        return

    name = args[0]
    hp = parse_int(args[1], "hp")
    if hp is None:
        return
    if hp < 1:
        return error("HP must be positive")

    damage_expr = args[2].lower()
    if not re.match(r"^\d+d\d+$", damage_expr):
        return error(f"Invalid damage dice: {damage_expr}", hint="Format: NdM, e.g. 3d6")

    attack_skill = parse_int(args[3], "attack_skill")
    if attack_skill is None:
        return
    if attack_skill < 1 or attack_skill > 20:
        return error(f"Attack skill must be 1-20, got {attack_skill}")

    drops = args[4].lower()
    valid_drops = ["junk", "common", "uncommon", "rare", "unique", "none"]
    if drops not in valid_drops:
        return error(f"Invalid drops tier: {drops}", valid_tiers=valid_drops)

    special = " ".join(args[5:]) if len(args) > 5 else ""

    state.setdefault("enemies", {})[name] = {
        "hp": hp,
        "max_hp": hp,
        "damage": damage_expr,
        "attack_skill": attack_skill,
        "drops": drops,
        "special": special,
        "status": "alive",
    }

    save_state(state)
    ok(f"Enemy added: {name}",
       enemy=name,
       hp=f"{hp}/{hp}",
       damage=damage_expr,
       attack_skill=attack_skill,
       drops=drops,
       special=special or "none")


def cmd_enemy_hurt(args):
    """Deal damage to an enemy (negative heals).
    Usage: enemy-hurt <name> <amount>
    On kill: auto-rolls loot from enemy's drops tier.
    """
    if len(args) < 2:
        return error("Usage: enemy-hurt <name> <amount>")

    state = require_state()
    if not state:
        return

    name = args[0]
    amount = parse_int(args[1], "amount")
    if amount is None:
        return

    enemy = require_enemy(state, name)
    if not enemy:
        return

    if enemy["status"] == "dead" and amount > 0:
        return error(f"{name} is already dead")

    old_hp = enemy["hp"]
    if amount < 0:
        # Heal
        enemy["hp"] = min(enemy["max_hp"], enemy["hp"] - amount)
        if enemy["status"] == "dead":
            enemy["status"] = "alive"
    else:
        enemy["hp"] = max(0, enemy["hp"] - amount)

    result = {
        "ok": True,
        "enemy": name,
        "hp_before": old_hp,
        "hp_after": enemy["hp"],
        "max_hp": enemy["max_hp"],
        "amount": amount,
    }

    if enemy["hp"] <= 0 and enemy["status"] != "dead":
        enemy["status"] = "dead"
        result["killed"] = True
        result["message"] = f"{name} has been defeated!"

        # Auto-roll loot
        if enemy["drops"] != "none":
            from .data import LOOT_TABLES
            import random
            tier = enemy["drops"]
            if tier in LOOT_TABLES:
                loot = random.choice(LOOT_TABLES[tier])
                result["loot_drop"] = {"tier": tier, "item": loot}

    save_state(state)
    output(result, indent=True)


def cmd_enemy_attack(args):
    """Enemy attacks a player. Rolls 1d20 vs attack_skill, auto-applies damage.
    Usage: enemy-attack <enemy> <target_player>
    Roll 1 = critical hit (bonus damage). Roll 20 = fumble (miss).
    """
    if len(args) < 2:
        return error("Usage: enemy-attack <enemy> <target_player>")

    state = require_state()
    if not state:
        return

    enemy_name = args[0]
    target_name = args[1]

    enemy = require_enemy(state, enemy_name)
    if not enemy:
        return
    if enemy["status"] == "dead":
        return error(f"{enemy_name} is dead and cannot attack")

    player = require_player(state, target_name)
    if not player:
        return

    # Roll 1d20 vs attack_skill
    attack_roll = roll_dice(1, 20)[0]
    hit = attack_roll <= enemy["attack_skill"]
    is_crit = attack_roll == 1
    is_fumble = attack_roll == 20

    result = {
        "ok": True,
        "attacker": enemy_name,
        "target": target_name,
        "attack_roll": attack_roll,
        "attack_skill": enemy["attack_skill"],
    }

    if is_fumble:
        result["hit"] = False
        result["fumble"] = True
        result["detail"] = f"Roll {attack_roll} -> Fumble! Miss + complication"
    elif hit:
        # Parse damage dice
        parts = enemy["damage"].split("d")
        dice_count = int(parts[0])
        dice_sides = int(parts[1])

        damage_dice = roll_dice(dice_count, dice_sides)
        total_damage = sum(damage_dice)

        if is_crit:
            crit_bonus = dice_count
            total_damage += crit_bonus
            result["critical"] = True
            result["crit_bonus"] = crit_bonus
            result["detail"] = f"Roll {attack_roll} -> Critical Hit! +{crit_bonus} bonus damage"
        else:
            result["detail"] = f"Roll {attack_roll} -> Hit"

        result["hit"] = True
        result["damage_dice"] = damage_dice
        result["total_damage"] = total_damage

        # Apply damage to player
        old_hp = player["hp"]
        player["hp"] = max(0, player["hp"] - total_damage)
        result["target_hp_before"] = old_hp
        result["target_hp_after"] = player["hp"]
        result["target_max_hp"] = player["max_hp"]

        if player["hp"] <= 0:
            result["target_down"] = True
            result["message"] = f"{target_name} is down! (Incapacitated — 3 turns to stabilize)"
            # Auto-add Incapacitated status
            effects = player.get("status_effects", [])
            if not any(e["name"] == "Incapacitated" for e in effects):
                effects.append({"name": "Incapacitated", "remaining": 3})
    else:
        result["hit"] = False
        result["detail"] = f"Roll {attack_roll} -> Miss (needed <={enemy['attack_skill']})"

    save_state(state)
    output(result, indent=True)


def cmd_enemy_list(args):
    """List all enemies with status.
    Usage: enemy-list
    """
    state = require_state()
    if not state:
        return

    enemies = state.get("enemies", {})
    if not enemies:
        return ok("No enemies on the battlefield")

    enemy_list = []
    for name, e in enemies.items():
        enemy_list.append({
            "name": name,
            "hp": f"{e['hp']}/{e['max_hp']}",
            "status": e["status"],
            "damage": e["damage"],
            "attack_skill": e["attack_skill"],
            "special": e.get("special", ""),
        })

    output({"ok": True, "enemies": enemy_list}, indent=True)


def cmd_enemy_clear(args):
    """Remove enemies from the battlefield.
    Usage: enemy-clear       (remove dead enemies only)
           enemy-clear all   (remove all enemies)
    """
    state = require_state()
    if not state:
        return

    enemies = state.get("enemies", {})
    if not enemies:
        return ok("No enemies to clear")

    clear_all = args and args[0].lower() == "all"

    if clear_all:
        removed = list(enemies.keys())
        state["enemies"] = {}
    else:
        removed = [n for n, e in enemies.items() if e["status"] == "dead"]
        for n in removed:
            del enemies[n]

    save_state(state)
    ok(f"Cleared {len(removed)} enemies" + (" (all)" if clear_all else " (dead)"),
       removed=removed,
       remaining=list(state.get("enemies", {}).keys()))
