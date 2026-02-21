"""Dice mechanics: rolls, skill checks, oracle, damage, initiative."""

import random
from .util import (
    error, ok, output, parse_int,
    require_state, require_player, validate_attr, validate_skill,
    roll_dice, save_state, get_effective_special,
)


def cmd_roll(args):
    """Roll dice. Usage: roll <NdM> e.g. roll 2d20, roll 3d6"""
    if not args:
        return error("Usage: roll <NdM>, e.g. roll 2d20")

    expr = args[0].lower()
    try:
        count_s, sides_s = expr.split("d")
        count = int(count_s) if count_s else 1
        sides = int(sides_s)
    except (ValueError, IndexError):
        return error(f"Invalid dice expression: {expr}", hint="Format: NdM, e.g. 2d20, 3d6")

    if count < 1 or count > 100:
        return error(f"Dice count must be 1-100, got {count}")
    if sides < 1 or sides > 100:
        return error(f"Dice sides must be 1-100, got {sides}")

    results = roll_dice(count, sides)
    output({
        "ok": True,
        "dice": expr,
        "results": results,
        "total": sum(results),
        "min": min(results),
        "max": max(results),
    })


# ---------------------------------------------------------------------------
# Skill Check (unified solo / assisted / group)
# ---------------------------------------------------------------------------

def _evaluate_die(die_value, target, is_tag, skill_val):
    """Evaluate a single d20 against a target number.
    Returns (successes, is_crit, is_complication, detail_str).
    """
    if die_value == 1:
        return 2, True, False, f"{die_value} -> Critical Success (+2)"
    elif die_value == 20:
        return 0, False, True, f"{die_value} -> Complication!"
    elif die_value <= target:
        if is_tag and die_value <= skill_val:
            return 2, True, False, f"{die_value} -> Success + Tag Crit (+1)"
        return 1, False, False, f"{die_value} -> Success"
    else:
        return 0, False, False, f"{die_value} -> Failure"


def _find_leader_name(state, player_names, attr, skill_name):
    """Find the player with highest target number (effective attr + skill)."""
    best_name = player_names[0]
    best_target = -1
    for pname in player_names:
        player = state["players"][pname]
        effective, _ = get_effective_special(player)
        target = effective.get(attr, 0) + player.get("skills", {}).get(skill_name, 0)
        if target > best_target:
            best_target = target
            best_name = pname
    return best_name


def _evaluate_check(state, player_names, attr, skill_name, difficulty, ap_dice=0):
    """Core check evaluation for solo, assisted, and group checks.

    Leader auto-selected as player with highest target number.
    Leader rolls 2d20 + AP dice. Each helper rolls 1d20. Total cap 5d20.
    Leader must score >=1 success for helper successes to count.
    """
    # Resolve all participants and compute targets using effective SPECIAL
    participants = []
    all_modifiers = {}
    for pname in player_names:
        player = state["players"][pname]
        effective, modifiers = get_effective_special(player)
        attr_val = effective.get(attr, 0)
        skill_val = player.get("skills", {}).get(skill_name, 0)
        is_tag = skill_name in player.get("tag_skills", [])
        target = attr_val + skill_val
        participants.append({
            "name": pname,
            "attr_val": attr_val,
            "skill_val": skill_val,
            "is_tag": is_tag,
            "target": target,
        })
        if modifiers:
            all_modifiers[pname] = modifiers

    # Auto-select leader (highest target, stable sort preserves input order on ties)
    participants.sort(key=lambda p: p["target"], reverse=True)
    leader = participants[0]
    helpers = participants[1:]

    # Mode detection
    if len(participants) == 1:
        mode = "solo"
    elif len(participants) == 2:
        mode = "assisted"
    else:
        mode = "group"

    # Dice pool: leader 2d20 + helpers 1d20 each + AP dice, cap 5
    base_count = 2 + len(helpers)
    total_count = min(5, base_count + ap_dice)
    actual_ap = total_count - base_count

    # Build dice pool description
    pool_parts = ["2 leader"]
    if helpers:
        pool_parts.append(f"{len(helpers)} helper{'s' if len(helpers) > 1 else ''}")
    if actual_ap > 0:
        pool_parts.append(f"{actual_ap} AP")

    # Roll all dice
    all_dice = roll_dice(total_count, 20)

    # Assign dice: first 2 = leader, next N = helpers, rest = AP (leader's)
    leader_dice = all_dice[:2] + all_dice[base_count:]
    helper_dice = all_dice[2:2 + len(helpers)]

    # Evaluate leader's dice
    leader_successes = 0
    leader_crits = 0
    leader_complications = 0
    leader_details = []

    for d in leader_dice:
        s, crit, comp, detail = _evaluate_die(d, leader["target"], leader["is_tag"], leader["skill_val"])
        leader_successes += s
        if crit:
            leader_crits += 1
        if comp:
            leader_complications += 1
        leader_details.append(detail)

    # Official rule: leader must get >=1 success for helper successes to count
    leader_contributed = leader_successes >= 1

    # Evaluate helper dice
    total_successes = leader_successes
    total_crits = leader_crits
    total_complications = leader_complications
    helper_results = []

    for i, h in enumerate(helpers):
        d = helper_dice[i]
        s, crit, comp, detail = _evaluate_die(d, h["target"], h["is_tag"], h["skill_val"])

        if leader_contributed:
            total_successes += s
        if crit:
            total_crits += 1
        if comp:
            total_complications += 1

        helper_results.append({
            "name": h["name"],
            "target": h["target"],
            "die": d,
            "detail": detail,
            "successes": s,
            "counted": leader_contributed,
        })

    passed = total_successes >= difficulty
    excess_ap = max(0, total_successes - difficulty) if passed else 0

    result = {
        "ok": True,
        "mode": mode,
        "players": [p["name"] for p in participants],
        "leader": leader["name"],
        "attribute": f"{attr} ({leader['attr_val']})",
        "skill": f"{skill_name} ({leader['skill_val']})" + (" [TAG]" if leader["is_tag"] else ""),
        "leader_target": leader["target"],
        "difficulty": difficulty,
        "dice_pool": f"{total_count}d20 ({' + '.join(pool_parts)})",
        "dice": all_dice,
        "leader_dice": leader_dice,
        "leader_details": leader_details,
        "leader_successes": leader_successes,
        "total_successes": total_successes,
        "crits": total_crits,
        "complications": total_complications,
        "passed": passed,
        "excess_ap": excess_ap,
        "verdict": "Success!" if passed else "Failure...",
    }

    if helpers:
        result["helpers"] = helper_results
        if not leader_contributed:
            result["leader_failed_note"] = "Leader failed — helper successes do not count!"

    if actual_ap > 0:
        result["ap_spent"] = actual_ap

    if all_modifiers:
        result["special_modifiers"] = {
            name: {attr: sum(m for _, m in mods) for attr, mods in player_mods.items()}
            for name, player_mods in all_modifiers.items()
        }

    if total_complications > 0:
        result["complication_note"] = "A complication occurred! Even on a success, trouble is brewing."

    return result, excess_ap


def cmd_check(args):
    """Skill check (unified solo/assisted/group).
    Usage: check <players> <attribute> <skill> <difficulty> [ap_spend]

    Players: comma-separated names (Jake or Jake,Sarah or Jake,Sarah,Bob)
    - 1 player: solo check (2d20)
    - 2 players: assisted check (3d20, leader auto-selected)
    - 3+ players: group check (leader 2d20 + helpers 1d20 each)

    AP: spend 0-3 AP for extra d20s (max 5d20 total).
    Leader auto-selected as player with highest target number.
    """
    if len(args) < 4:
        return error(
            "Usage: check <players> <attribute> <skill> <difficulty> [ap_spend]",
            hint="Examples: check Jake PER Lockpick 2 | check Jake,Sarah PER Lockpick 3 1",
        )

    state = require_state()
    if not state:
        return

    # Parse comma-separated player names
    player_names = [n.strip() for n in args[0].split(",") if n.strip()]
    if not player_names:
        return error("At least one player name required")

    # Validate all players exist
    for pname in player_names:
        if not require_player(state, pname):
            return

    attr = validate_attr(args[1])
    if not attr:
        return

    skill_name = validate_skill(args[2])
    if not skill_name:
        return

    difficulty = parse_int(args[3], "difficulty")
    if difficulty is None:
        return
    if difficulty < 0:
        return error(f"Difficulty must be non-negative, got {difficulty}")

    # Optional AP spend
    ap_spend = 0
    if len(args) > 4:
        ap_spend = parse_int(args[4], "ap_spend")
        if ap_spend is None:
            return
        if ap_spend < 0 or ap_spend > 3:
            return error("AP spend must be 0-3", hint="Each AP adds 1d20, max 5d20 total")

    # Validate dice cap: leader(2) + helpers(N-1) + AP <= 5
    base_dice = len(player_names) + 1
    if base_dice > 5:
        return error(f"Too many players ({len(player_names)}). Max 4 per check (5d20 cap)")
    max_ap = 5 - base_dice
    if ap_spend > max_ap:
        if max_ap <= 0:
            return error(f"Cannot spend AP — already at {base_dice}d20 with {len(player_names)} players")
        return error(f"Max {max_ap} AP with {len(player_names)} players (5d20 cap)")

    # Find leader and validate/deduct AP
    leader_name = _find_leader_name(state, player_names, attr, skill_name)
    leader_player = state["players"][leader_name]
    original_ap = leader_player.get("ap", 0)

    if ap_spend > 0:
        if original_ap < ap_spend:
            return error(f"{leader_name} has {original_ap} AP, cannot spend {ap_spend}",
                         hint="Earn AP from excess successes on checks")
        leader_player["ap"] -= ap_spend

    # Roll
    result, excess_ap = _evaluate_check(state, player_names, attr, skill_name, difficulty, ap_spend)

    # Add excess AP to leader
    if excess_ap > 0:
        leader_player["ap"] += excess_ap

    # Track AP change in output
    result["ap_before"] = original_ap
    result["ap_after"] = leader_player["ap"]
    result["ap_change"] = leader_player["ap"] - original_ap

    save_state(state)
    output(result, indent=True)


def cmd_oracle(args):
    """Oracle D6 for narrative judgment."""
    result = random.randint(1, 6)
    meanings = {
        1: "No, and things get worse",
        2: "No",
        3: "No, but there's a silver lining",
        4: "Yes, but at a cost",
        5: "Yes",
        6: "Yes, and something extra",
    }
    output({"ok": True, "oracle_d6": result, "meaning": meanings[result]})


# ---------------------------------------------------------------------------
# Damage
# ---------------------------------------------------------------------------

def cmd_damage(args):
    """Roll combat damage dice.
    Usage: damage <player> <dice_count> [bonus] [ap_spend]
    Each AP spent adds 1d6 (max 3 AP).
    Example: damage Jake 3 2 1  (3d6 + 2 bonus, spending 1 AP for 4d6 total)
    """
    if len(args) < 2:
        return error("Usage: damage <player> <dice_count> [bonus] [ap_spend]",
                      hint="Example: damage Jake 3 2")

    state = require_state()
    if not state:
        return

    player_name = args[0]
    player = require_player(state, player_name)
    if not player:
        return

    count = parse_int(args[1], "dice_count")
    if count is None:
        return
    if count < 1 or count > 20:
        return error(f"Dice count must be 1-20, got {count}")

    bonus = 0
    if len(args) > 2:
        bonus = parse_int(args[2], "bonus")
        if bonus is None:
            return

    ap_spend = 0
    if len(args) > 3:
        ap_spend = parse_int(args[3], "ap_spend")
        if ap_spend is None:
            return
        if ap_spend < 0 or ap_spend > 3:
            return error("AP spend must be 0-3", hint="Each AP adds 1d6")

    # Validate and deduct AP
    original_ap = player.get("ap", 0)
    if ap_spend > 0:
        if original_ap < ap_spend:
            return error(f"{player_name} has {original_ap} AP, cannot spend {ap_spend}")
        player["ap"] -= ap_spend

    total_dice = count + ap_spend
    dice = roll_dice(total_dice, 6)
    total_damage = bonus
    effects = []
    details = []

    for d in dice:
        if d in (1, 2):
            total_damage += 1
            details.append(f"{d} -> 1 damage")
        elif d in (3, 4):
            total_damage += 2
            details.append(f"{d} -> 2 damage")
        elif d in (5, 6):
            total_damage += 3
            effects.append("Special Effect")
            details.append(f"{d} -> 3 damage + Special Effect!")

    result = {
        "ok": True,
        "player": player_name,
        "dice": dice,
        "details": details,
        "base_damage": total_damage - bonus,
        "bonus": bonus,
        "total_damage": total_damage,
        "effects": effects,
    }

    if ap_spend > 0:
        result["ap_spent"] = ap_spend
        result["ap_before"] = original_ap
        result["ap_after"] = player["ap"]

    if ap_spend > 0:
        save_state(state)

    output(result, indent=True)


# ---------------------------------------------------------------------------
# Initiative
# ---------------------------------------------------------------------------

def cmd_initiative(args):
    """Calculate combat initiative order for all players and alive enemies."""
    state = require_state()
    if not state:
        return

    players = state.get("players", {})
    if not players:
        return error("No players in the game")

    order = []
    for name, player in players.items():
        effective, _ = get_effective_special(player)
        per = effective.get("PER", 5)
        agi = effective.get("AGI", 5)
        init_val = per + agi
        tiebreaker = random.randint(1, 20)
        order.append({"name": name, "type": "player", "character": player.get("character", name), "initiative": init_val, "tiebreaker": tiebreaker})

    # Include alive enemies (use attack_skill as initiative value)
    for name, enemy in state.get("enemies", {}).items():
        if enemy.get("status") == "alive":
            tiebreaker = random.randint(1, 20)
            order.append({"name": name, "type": "enemy", "initiative": enemy.get("attack_skill", 5), "tiebreaker": tiebreaker})

    order.sort(key=lambda x: (x["initiative"], x["tiebreaker"]), reverse=True)
    output({"ok": True, "initiative_order": order}, indent=True)
