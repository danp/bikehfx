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

    # Extract day for chart title
    day_label = parsed.get("day", "Unknown Date")

    # Prepare data records
    hour_order = [f"{i:02}" for i in range(24)]
    records = []
    counter_names = set()

    for counter in parsed.get("counters", []):
        name = counter.get("name")
        counter_names.add(name)

        if counter.get("missing", False):
            for hour in hour_order:
                records.append({"counter": name, "hour": hour, "count": np.nan})
        else:
            for hour_info in counter.get("hours", []):
                hour = f"{hour_info['hour']:02}"
                count = hour_info["count"]
                records.append({"counter": name, "hour": hour, "count": count})

    df = pd.DataFrame(records)

    # Pivot to heatmap format
    heatmap_data = df.pivot(index="counter", columns="hour", values="count")
    heatmap_data = heatmap_data.reindex(columns=hour_order)
    heatmap_data = heatmap_data.reindex(sorted(counter_names))

    # Annotation formatting: show integer, blank if NaN or 0
    annotations = heatmap_data.copy()
    annotations = annotations.where(~annotations.isna() & (annotations != 0))
    annotations = annotations.map(lambda v: f"{int(v)}" if pd.notna(v) else "")

    # Set up layout
    num_counters = heatmap_data.shape[0]
    cell_size = 0.6
    width = 24 * cell_size
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
    ax.set_title(f"Counts for {day_label} by hour starting", fontsize=14, weight='bold', pad=10)
    ax.set_xlabel("Hour", fontsize=12, labelpad=5)
    ax.set_ylabel("Counter", fontsize=12, labelpad=5)

    ax.set_xticks(np.arange(24) + 0.5)
    ax.set_xticklabels(hour_order, ha='center', fontsize=12)
    ax.set_yticklabels(ax.get_yticklabels(), rotation=0, fontsize=12)

    plt.tight_layout()

    # Output PNG to stdout
    buffer = BytesIO()
    plt.savefig(buffer, format='png', dpi=300, bbox_inches='tight')
    buffer.seek(0)
    sys.stdout.buffer.write(buffer.read())

if __name__ == "__main__":
    main()
