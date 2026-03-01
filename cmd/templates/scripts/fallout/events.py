"""Random generation: loot, trade, NPC."""

import random
from .util import error, output, parse_int, load_state, save_state, require_state, require_player, get_effective_special
from .data import (
    LOOT_TABLES,
    NPC_SURNAMES, NPC_NAMES, NPC_BUILDS, NPC_FEATURES,
    NPC_CLOTHES, NPC_MOTIVES, NPC_KNOWLEDGE, NPC_SPEECH,
)


def cmd_loot(args):
    """Generate random loot."""
    tier = args.tier.lower() if args.tier else None
    random_tier = args.random_tier
    count = args.count

    valid_tiers = list(LOOT_TABLES.keys())
    hint = "Use 'inventory <player> add <item>' to give loot to a player."

    # No tier and no --random-tier: show available tiers
    if tier is None and not random_tier:
        return output({
            "ok": True,
            "available_tiers": {t: len(v) for t, v in LOOT_TABLES.items()},
            "hint": "Specify a tier: loot <tier> --count N | Or use: loot --random-tier --count N",
        }, indent=True)

    # --count is required
    if count is None:
        return error("--count N is required", hint="Example: loot rare --count 3 | loot --random-tier --count 2")
    count = max(1, min(count, 10))

    if random_tier:
        # Weighted random tier per item
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
        output({"ok": True, "loot": items, "hint": hint}, indent=True)
    elif tier in LOOT_TABLES:
        pool = LOOT_TABLES[tier]
        items = random.sample(pool, min(count, len(pool)))
        output({"ok": True, "tier": tier, "items": items, "hint": hint}, indent=True)
    else:
        error(f"Unknown tier: {tier}", valid_tiers=valid_tiers,
              hint="Example: loot rare --count 3 | loot --random-tier --count 2")


def cmd_trade(args):
    """Calculate trade price."""
    state = require_state()
    if not state:
        return

    name = args.player
    base = args.base_price
    if base < 1:
        return error("Base price must be positive",
                      hint="Base price is the item's standard value in caps before CHA/Barter modifiers.")

    action = args.action.lower()
    if action not in ("buy", "sell"):
        return error("Action must be 'buy' or 'sell'",
                      hint="'buy' = player purchases (price reduced by CHA/Barter). 'sell' = player sells (price increased by CHA/Barter).")

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
    """Generate random NPC(s)."""
    count = max(1, min(args.count, 5))

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

    # Auto-persist to state if game is initialized.
    state = load_state()
    if state is not None:
        if "npcs" not in state:
            state["npcs"] = {}
        for npc in npcs:
            state["npcs"][npc["name"]] = {
                "appearance": npc["appearance"],
                "motive": npc["motive"],
                "knowledge": npc["knowledge"],
                "speech_style": npc["speech_style"],
                "met_turn": state.get("turn", 0),
                "location": state.get("location", ""),
            }
        save_state(state)

    output({"ok": True, "npcs": npcs, "hint": "NPC(s) auto-registered to game state. Use 'status' to review."}, indent=True)
