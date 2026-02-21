"""Random generation: events, loot, trade, NPC, weather, help."""

import random
from .util import error, ok, output, parse_int, require_state, require_player, save_state, get_effective_special
from .data import (
    ENCOUNTERS, LOOT_TABLES, ATMOSPHERIC, QUEST_HOOKS,
    NPC_SURNAMES, NPC_NAMES, NPC_BUILDS, NPC_FEATURES,
    NPC_CLOTHES, NPC_MOTIVES, NPC_KNOWLEDGE, NPC_SPEECH,
    WEATHER_TABLE,
)


def cmd_event(args):
    """Generate a random event.
    Usage: event [category]
    Categories: wasteland, urban, vault, interior, special, atmospheric, quest
    No category = random from all pools (encounters + atmospheric + quest)
    """
    category = args[0].lower() if args else None

    if category == "atmospheric":
        event = random.choice(ATMOSPHERIC)
        output({"ok": True, "type": "atmospheric", "event": event}, indent=True)
        return
    elif category == "quest":
        quest = random.choice(QUEST_HOOKS)
        output({"ok": True, "type": "quest_hook", "quest": quest}, indent=True)
        return
    elif category == "special":
        if "special" in ENCOUNTERS:
            enc = random.choice(ENCOUNTERS["special"])
            output({"ok": True, "type": "special_encounter", "encounter": enc}, indent=True)
        else:
            error("No special encounter data available")
        return
    elif category in ENCOUNTERS:
        pool = ENCOUNTERS[category]
        encounter = random.choice(pool)
        output({"ok": True, "type": "encounter", "category": category, "encounter": encounter}, indent=True)
        return
    elif category is None:
        # Pick from ALL pools: encounters + atmospheric + quest
        # Weight: encounters 70%, atmospheric 15%, quest 15%
        roll = random.randint(1, 100)
        if roll <= 70:
            pool = []
            for v in ENCOUNTERS.values():
                pool.extend(v)
            encounter = random.choice(pool)
            output({"ok": True, "type": "encounter", "category": "random", "encounter": encounter}, indent=True)
        elif roll <= 85:
            event = random.choice(ATMOSPHERIC)
            output({"ok": True, "type": "atmospheric", "event": event}, indent=True)
        else:
            quest = random.choice(QUEST_HOOKS)
            output({"ok": True, "type": "quest_hook", "quest": quest}, indent=True)
    else:
        valid = list(ENCOUNTERS.keys()) + ["atmospheric", "quest"]
        if "special" not in valid:
            valid.append("special")
        error(f"Unknown category: {category}", valid_categories=valid)


def cmd_loot(args):
    """Generate random loot.
    Usage: loot [tier] [count]
    Tiers: junk, common, uncommon, rare, unique
    No tier = weighted random
    """
    tier = args[0].lower() if args else None
    count = 1
    if len(args) > 1:
        count = parse_int(args[1], "count")
        if count is None:
            return
    count = max(1, min(count, 10))

    if tier and tier in LOOT_TABLES:
        pool = LOOT_TABLES[tier]
        items = random.sample(pool, min(count, len(pool)))
        output({"ok": True, "tier": tier, "items": items}, indent=True)
    elif tier is None:
        weights = {"junk": 35, "common": 35, "uncommon": 20, "rare": 8, "unique": 2}
        items = []
        for _ in range(count):
            roll = random.randint(1, 100)
            cumulative = 0
            chosen_tier = "junk"
            for t, w in weights.items():
                cumulative += w
                if roll <= cumulative:
                    chosen_tier = t
                    break
            items.append({"tier": chosen_tier, "item": random.choice(LOOT_TABLES[chosen_tier])})
        output({"ok": True, "loot": items}, indent=True)
    else:
        valid = list(LOOT_TABLES.keys())
        error(f"Unknown tier: {tier}", valid_tiers=valid)


def cmd_trade(args):
    """Calculate trade price.
    Usage: trade <player> <base_price> buy/sell
    """
    if len(args) < 3:
        return error("Usage: trade <player> <base_price> buy/sell",
                      hint="Example: trade Jake 100 buy")

    state = require_state()
    if not state:
        return

    name = args[0]
    base = parse_int(args[1], "base_price")
    if base is None:
        return
    if base < 1:
        return error("Base price must be positive")

    action = args[2].lower()
    if action not in ("buy", "sell"):
        return error("Action must be 'buy' or 'sell'")

    player = require_player(state, name)
    if not player:
        return

    effective, _ = get_effective_special(player)
    cha = effective.get("CHA", 5)
    barter = player.get("skills", {}).get("Barter", 0)
    modifier = 1.0 - (cha - 5) * 0.05 - barter * 0.05

    if action == "buy":
        price = max(1, round(base * max(0.5, modifier)))
    else:
        price = max(1, round(base * min(1.5, 2.0 - modifier)))

    discount_pct = round((1 - price / base) * 100) if action == "buy" else round((price / base - 1) * 100)
    output({
        "ok": True,
        "player": name,
        "action": "Buy" if action == "buy" else "Sell",
        "base_price": base,
        "cha": cha,
        "barter_skill": barter,
        "final_price": price,
        "discount": f"{discount_pct}%" if action == "buy" else f"{discount_pct:+d}%",
    }, indent=True)


def cmd_npc_gen(args):
    """Generate random NPC(s).
    Usage: npc-gen [count]
    """
    count = 1
    if args:
        count = parse_int(args[0], "count")
        if count is None:
            return
    count = max(1, min(count, 5))

    npcs = []
    for _ in range(count):
        surname = random.choice(NPC_SURNAMES)
        first = random.choice(NPC_NAMES)
        full_name = f"{surname} {first}"
        build = random.choice(NPC_BUILDS)
        feature = random.choice(NPC_FEATURES)
        clothes = random.choice(NPC_CLOTHES)
        motive = random.choice(NPC_MOTIVES)
        knowledge = random.choice(NPC_KNOWLEDGE)
        speech = random.choice(NPC_SPEECH)

        npcs.append({
            "name": full_name,
            "appearance": f"{build}, {feature}, wearing {clothes}",
            "motive": motive,
            "knowledge": knowledge,
            "speech_style": speech,
        })

    # Always use consistent format: {"npcs": [...]}
    output({"ok": True, "npcs": npcs}, indent=True)


def cmd_weather(args):
    """Generate weather.
    Usage: weather [set]
    If 'set' is provided, also saves to game state.
    """
    total = sum(w["weight"] for w in WEATHER_TABLE)
    roll = random.randint(1, total)
    cumulative = 0
    chosen = WEATHER_TABLE[0]
    for w in WEATHER_TABLE:
        cumulative += w["weight"]
        if roll <= cumulative:
            chosen = w
            break

    result = {
        "ok": True,
        "weather": chosen["weather"],
        "description": chosen["desc"],
        "effect": chosen["effect"],
    }

    if args and args[0].lower() == "set":
        state = require_state()
        if state:
            state["weather"] = chosen["weather"]
            save_state(state)
            result["saved"] = True

    output(result, indent=True)


def cmd_help(args):
    """Show all available commands as JSON."""
    commands = {
        "init": "Initialize a new game",
        "status [player]": "View game/player status",
        "add-player <player_id> <name> <char> <bg> S P E C I A L skill1 skill2 skill3": "Add player (player_id = Discord username/ID)",
        "remove-player <name>": "Remove player",
        "roll <NdM>": "Roll dice (e.g. 2d20, 3d6)",
        "check <players> <attr> <skill> <difficulty> [ap_spend]": "Skill check (solo/assisted/group, comma-separated players)",
        "oracle": "Oracle D6 narrative judgment",
        "damage <player> <count> [bonus] [ap_spend]": "Combat damage dice",
        "initiative": "Calculate initiative order",
        "hurt <player> <amount>": "Deal damage",
        "heal <player> <amount>": "Heal player",
        "rads <player> <amount>": "Modify radiation",
        "caps <player> <amount>": "Modify caps",
        "ap <player> <amount>": "Modify action points",
        "inventory <player> add/remove <item> [qty]": "Manage inventory (auto-stack, qty default 1)",
        "use-item <player> <item>": "Use consumable",
        "effect <player> add/remove/list <name> [duration]": "Manage status effects",
        "rest [hours]": "Rest and recover (default 8h)",
        "set <field> <value>": "Set game state field",
        "flag add/remove/list [name]": "Manage story flags",
        "turn": "Advance turn (auto-cycles time, ticks effects)",
        "log <event>": "Log event",
        "event [category]": "Random encounter (wasteland/urban/vault/interior/special/atmospheric/quest)",
        "loot [tier] [count]": "Random loot (junk/common/uncommon/rare/unique)",
        "trade <player> <price> buy/sell": "Calculate trade price",
        "skill-up <player> <skill> [amount]": "Increase skill level",
        "npc-gen [count]": "Generate random NPC",
        "weather [set]": "Generate weather",
        "recover": "Restore from backup",
        "enemy-add <name> <hp> <damage> <attack_skill> <drops> [special]": "Add enemy",
        "enemy-hurt <name> <amount>": "Damage enemy (negative heals)",
        "enemy-attack <enemy> <target_player>": "Enemy attacks player (1d20, auto-damage)",
        "enemy-list": "List all enemies",
        "enemy-clear [all]": "Remove dead (or all) enemies",
        "help": "Show this help",
    }
    output({"ok": True, "commands": commands}, indent=True)
