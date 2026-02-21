"""Enemy tracking: add, hurt, attack."""

import re
from .util import (
    error, ok, output, parse_int,
    require_state, require_player, require_enemy,
    roll_dice, save_state,
)


def _find_template(name):
    """Case-insensitive template lookup."""
    from .data import ENEMY_TEMPLATES
    for tname, tdata in ENEMY_TEMPLATES.items():
        if tname.lower() == name.lower():
            return tname, tdata
    return None, None


def cmd_enemy_add(args):
    """Add an enemy to the battlefield.
    Usage:
      enemy-add <template>                    # e.g. enemy-add Raider
      enemy-add <name> <template>             # e.g. enemy-add "Raider 1" Raider
      enemy-add <name> <hp> <damage> <skill> <drops> [special]  # full custom
    """
    if not args:
        from .data import ENEMY_TEMPLATES
        return error("Usage: enemy-add <template> | enemy-add <name> <template> | enemy-add <name> <hp> <damage> <skill> <drops>",
                      templates=list(ENEMY_TEMPLATES.keys()))

    state = require_state()
    if not state:
        return

    # Mode detection by arg count
    template = None
    if len(args) == 1:
        # Template mode: enemy-add Raider
        tname, template = _find_template(args[0])
        if not template:
            from .data import ENEMY_TEMPLATES
            return error(f"Unknown template: {args[0]}", templates=list(ENEMY_TEMPLATES.keys()))
        name = tname
        hp = template["hp"]
        damage_expr = template["damage"]
        attack_skill = template["attack_skill"]
        drops = template["drops"]
        special = template["special"]

    elif len(args) == 2:
        # Named template: enemy-add "Raider 1" Raider
        tname, template = _find_template(args[1])
        if template:
            name = args[0]
            hp = template["hp"]
            damage_expr = template["damage"]
            attack_skill = template["attack_skill"]
            drops = template["drops"]
            special = template["special"]
        else:
            from .data import ENEMY_TEMPLATES
            return error(f"Unknown template: {args[1]}", templates=list(ENEMY_TEMPLATES.keys()))

    elif len(args) >= 5:
        # Full custom: enemy-add "Boss" 50 4d6 14 rare "special"
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
    else:
        from .data import ENEMY_TEMPLATES
        return error("Usage: enemy-add <template> | enemy-add <name> <template> | enemy-add <name> <hp> <damage> <skill> <drops>",
                      templates=list(ENEMY_TEMPLATES.keys()))

    # --- Encounter budget validation ---
    from .data import ENCOUNTER_RULES, hp_to_tier

    chapter = state.get("chapter", 1)
    rules = ENCOUNTER_RULES.get(chapter, ENCOUNTER_RULES[min(chapter, 6)])
    max_tier = rules["max_tier"]
    base_budget = rules["hp_budget"]
    safe_turns = rules["safe_turns"]

    # Determine tier
    if template:
        tier = template.get("tier", hp_to_tier(hp))
    else:
        tier = hp_to_tier(hp)

    # Tier check
    if tier > max_tier:
        from .data import ENEMY_TEMPLATES
        allowed = [t for t, d in ENEMY_TEMPLATES.items() if d.get("tier", 1) <= max_tier]
        return error(f"Enemy tier {tier} exceeds chapter {chapter} max tier {max_tier}",
                      allowed_templates=allowed)

    # Safe turns check (only tier 1 allowed during protection period)
    chapter_start = state.get("chapter_start_turn", 0)
    turns_in_chapter = state.get("turn", 0) - chapter_start
    if tier >= 2 and turns_in_chapter < safe_turns:
        return error(f"Protection period: only tier 1 enemies allowed for {safe_turns - turns_in_chapter} more turn(s)",
                      chapter=chapter, turns_in_chapter=turns_in_chapter, safe_turns=safe_turns)

    # Enemy count limit by days into chapter (1 day = 24 turns)
    alive_enemies = [e for e in state.get("enemies", {}).values() if e["status"] == "alive"]
    alive_count = len(alive_enemies)
    days_in_chapter = turns_in_chapter // 24
    if days_in_chapter < 1:
        max_enemies = 1
    elif days_in_chapter < 2:
        max_enemies = 2
    else:
        max_enemies = None
    if max_enemies is not None and alive_count >= max_enemies:
        return error(f"Enemy count limit: max {max_enemies} alive enemies on chapter day {days_in_chapter + 1}",
                      alive=alive_count, max=max_enemies)

    # HP budget check (scaled by player count)
    player_count = max(1, len(state.get("players", {})))
    effective_budget = int(base_budget * (1 + 0.5 * (player_count - 1)))
    alive_hp = sum(e["hp"] for e in alive_enemies)
    remaining_budget = effective_budget - alive_hp

    if hp > remaining_budget:
        return error(f"HP budget exceeded: {alive_hp}+{hp}={alive_hp + hp} > {effective_budget}",
                      budget=effective_budget, alive_hp=alive_hp, remaining=remaining_budget,
                      hint=f"Chapter {chapter} budget: {base_budget} base × {player_count} players = {effective_budget}")

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
       tier=tier,
       hp=f"{hp}/{hp}",
       damage=damage_expr,
       attack_skill=attack_skill,
       drops=drops,
       special=special or "none",
       budget=f"{alive_hp + hp}/{effective_budget}")


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


