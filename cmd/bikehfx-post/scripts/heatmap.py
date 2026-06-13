# /// script
# requires-python = ">=3.13"
# dependencies = ["pandas", "matplotlib", "seaborn", "numpy"]
# ///

import sys
import json
import pandas as pd
import matplotlib.pyplot as plt
import seaborn as sns
import numpy as np
from io import BytesIO


def build_records(parsed):
    x_values = parsed.get("x_values", [])
    records = []
    counter_order = []
    seen_counters = set()

    for counter in parsed.get("counters", []):
        name = counter.get("name", "Unknown")
        if name not in seen_counters:
            counter_order.append(name)
            seen_counters.add(name)

        if counter.get("missing", False):
            for key in x_values:
                records.append({"counter": name, "x": key, "count": np.nan})
            continue

        for value in counter.get("values", []):
            key = value.get("x")
            if key not in x_values:
                continue
            records.append({"counter": name, "x": key, "count": value.get("count")})

    return records, counter_order, x_values


def prepare_heatmap_data(records, counter_order, x_values, sort_counters):
    if sort_counters:
        counter_order = sorted(counter_order)

    data = pd.DataFrame(np.nan, index=counter_order or [], columns=x_values, dtype=float)

    for record in records:
        counter = record["counter"]
        x_key = record["x"]
        count = record["count"]

        if x_key not in x_values:
            continue

        if counter not in data.index:
            data.loc[counter] = np.nan

        if pd.isna(count):
            data.at[counter, x_key] = np.nan
            continue

        existing = data.at[counter, x_key]
        if pd.isna(existing):
            data.at[counter, x_key] = count
        else:
            data.at[counter, x_key] = existing + count

    # Ensure all specified counters are present even if there were no records
    if not data.index.empty and counter_order:
        data = data.reindex(counter_order)

    data = data.reindex(columns=x_values)

    return data


def build_annotations(data, enabled):
    if not enabled:
        return False

    annotations = data.copy()
    annotations = annotations.where(~annotations.isna() & (annotations != 0))
    return annotations.map(lambda v: f"{int(v)}" if pd.notna(v) else "")


def scale_color_data(data, scale):
    if scale == "sqrt":
        return np.sqrt(data)

    return data


def color_scale_bounds(data, scale):
    if scale == "sqrt":
        finite = data.to_numpy(dtype=float)
        finite = finite[np.isfinite(finite)]
        if finite.size == 0:
            return None, None
        return 0, np.sqrt(finite.max())

    return None, None


def validate_color_scale(scale):
    if scale not in ("", "linear", "sqrt"):
        raise ValueError(f"unsupported color scale: {scale}")


def set_ticks(ax, parsed, x_values):
    font_size = parsed.get("x_tick_font", 12)
    rotation = parsed.get("x_tick_rotation", 0)

    x_ticks = parsed.get("x_ticks")
    if x_ticks:
        positions = []
        labels = []
        for tick in x_ticks:
            pos = tick.get("position")
            label = tick.get("label", "")
            if pos is None or pos < 0 or pos >= len(x_values):
                continue
            positions.append(pos + 0.5)
            labels.append(label)

        if positions:
            ax.set_xticks(positions)
            ax.set_xticklabels(labels, ha="center", fontsize=font_size, rotation=rotation)
            return

    ax.set_xticks(np.arange(len(x_values)) + 0.5)
    ax.set_xticklabels(x_values, ha="center", fontsize=font_size, rotation=rotation)


def main():
    parsed = json.load(sys.stdin)

    records, counter_order, x_values = build_records(parsed)
    heatmap_data = prepare_heatmap_data(
        records, counter_order, x_values, parsed.get("sort_counters", True)
    )

    annotations = build_annotations(heatmap_data, parsed.get("annotations", True))
    color_scale = parsed.get("color_scale", "linear")
    validate_color_scale(color_scale)
    color_data = scale_color_data(heatmap_data, color_scale)
    vmin, vmax = color_scale_bounds(heatmap_data, color_scale)

    num_counters = max(len(color_data.index), 1)
    num_columns = max(len(x_values), 1)

    cell_width = parsed.get("cell_width", 0.6)
    cell_height = parsed.get("cell_height", cell_width)
    square = parsed.get("square", True)

    width = num_columns * cell_width
    height = num_counters * cell_height

    fig = plt.figure(figsize=(width, height), dpi=300)
    ax = fig.add_subplot(111)

    sns.heatmap(
        color_data,
        cmap="viridis",
        linewidths=0.2,
        linecolor="gray",
        annot=annotations,
        fmt="",
        square=square,
        ax=ax,
        mask=color_data.isna(),
        cbar=False,
        vmin=vmin,
        vmax=vmax,
        annot_kws={"fontsize": parsed.get("annotation_font", 10)},
    )

    ax.set_title(parsed.get("title", ""), fontsize=parsed.get("title_font", 14), weight="bold", pad=10)
    axis_font = parsed.get("axis_font", 12)
    ax.set_xlabel(parsed.get("x_label", ""), fontsize=axis_font, labelpad=5)
    ax.set_ylabel(parsed.get("y_label", ""), fontsize=axis_font, labelpad=5)

    set_ticks(ax, parsed, x_values)
    ax.set_yticklabels(ax.get_yticklabels(), rotation=0, fontsize=parsed.get("y_tick_font", 12))

    plt.tight_layout()

    buffer = BytesIO()
    plt.savefig(buffer, format="png", dpi=300, bbox_inches="tight")
    buffer.seek(0)
    sys.stdout.buffer.write(buffer.read())


if __name__ == "__main__":
    main()
