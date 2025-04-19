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

def main():
    # Read JSON input from stdin
    raw_input = sys.stdin.read()
    parsed = json.loads(raw_input)

    # Extract label for chart title
    week_label = parsed.get("week", "Unknown Week")

    # Prepare data records
    day_order = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]
    records = []
    counter_names = set()

    for counter in parsed.get("counters", []):
        name = counter.get("name")
        counter_names.add(name)

        if counter.get("missing", False):
            for day in day_order:
                records.append({"counter": name, "day": day, "count": np.nan})
        else:
            for day_info in counter.get("days", []):
                day = day_info["day"]
                count = day_info["count"]
                records.append({"counter": name, "day": day, "count": count})

    df = pd.DataFrame(records)

    # Pivot to heatmap format
    heatmap_data = df.pivot(index="counter", columns="day", values="count")
    heatmap_data = heatmap_data.reindex(columns=day_order)
    heatmap_data = heatmap_data.reindex(sorted(counter_names))

    # Annotation formatting: show integer, blank if NaN or 0
    annotations = heatmap_data.copy()
    annotations = annotations.where(~annotations.isna() & (annotations != 0))
    annotations = annotations.map(lambda v: f"{int(v)}" if pd.notna(v) else "")

    # Set up layout
    num_counters = heatmap_data.shape[0]
    cell_size = 0.8
    width = 7 * cell_size
    height = num_counters * cell_size

    fig = plt.figure(figsize=(width, height), dpi=300)
    ax = fig.add_subplot(111)

    # Draw heatmap with no colorbar
    sns.heatmap(
        heatmap_data,
        cmap="viridis",
        linewidths=0.2,
        linecolor='gray',
        annot=annotations,
        fmt='',
        square=True,
        ax=ax,
        mask=heatmap_data.isna(),
        cbar=False
    )

    # Titles and labels
    ax.set_title(f"Counts ending {week_label} by day", fontsize=14, weight='bold', pad=10)
    ax.set_xlabel("Day", fontsize=12, labelpad=5)
    ax.set_ylabel("Counter", fontsize=12, labelpad=5)

    ax.set_xticks(np.arange(7) + 0.5)
    ax.set_xticklabels(day_order, ha='center', fontsize=12)
    ax.set_yticklabels(ax.get_yticklabels(), rotation=0, fontsize=12)

    plt.tight_layout()

    # Output PNG to stdout
    buffer = BytesIO()
    plt.savefig(buffer, format='png', dpi=300, bbox_inches='tight')
    buffer.seek(0)
    sys.stdout.buffer.write(buffer.read())

if __name__ == "__main__":
    main()
