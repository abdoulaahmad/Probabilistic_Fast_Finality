import pandas as pd
import matplotlib.pyplot as plt
import seaborn as sns
import os

# Set aesthetic style for the plots
sns.set_theme(style="whitegrid", context="paper", font_scale=1.2)

def analyze_and_plot(csv_file='results.csv'):
    if not os.path.exists(csv_file):
        print(f"Error: {csv_file} not found. Please run the tests first.")
        return

    # Read the data
    df = pd.read_csv(csv_file)
    
    print(f"Loaded {len(df)} rounds from {csv_file}")
    
    # Create output directory for figures
    os.makedirs("figures", exist_ok=True)
    
    # -----------------------------------------------------------------
    # Graph 1: Suite C.1 Noise Impact (Boxplot)
    # Compares Baseline vs. C.1_Jitter
    # -----------------------------------------------------------------
    print("Generating Graph 1: Noise Impact...")
    suites_to_compare = ['A3_Baseline', 'C1_Jitter']
    df_noise = df[df['TestSuite'].isin(suites_to_compare)]
    
    if len(df_noise) > 0:
        plt.figure(figsize=(8, 6))
        # hue= is required alongside palette= in seaborn >= 0.14; legend is
        # redundant here since the x-axis already labels each suite.
        ax = sns.boxplot(x='TestSuite', y='LatencyMS', hue='TestSuite',
                         data=df_noise, palette="Set2", legend=False)
        sns.stripplot(x='TestSuite', y='LatencyMS', data=df_noise, color=".25", alpha=0.5, size=3)
        
        plt.title('PFF Latency Distribution: Baseline vs Network Jitter (Suite C.1)', fontsize=14, weight='bold')
        plt.ylabel('Time-to-Finality (ms)', fontsize=12)
        plt.xlabel('Test Suite Environment', fontsize=12)
        
        plt.tight_layout()
        plt.savefig('figures/Graph_1_Noise_Impact.png', dpi=300)
        plt.close()
        print(" -> Saved figures/Graph_1_Noise_Impact.png")
    else:
        print(" -> Skipped Graph 1 (no data for A3_Baseline or C1_Jitter)")

    # -----------------------------------------------------------------
    # Graph 2: Suite C.3 Protocol Resumption (Timeline)
    # Plots RoundID vs Latency for the C.3 partition test
    # -----------------------------------------------------------------
    print("Generating Graph 2: Protocol Resumption...")
    df_resumption = df[df['TestSuite'] == 'C3_Partition']
    
    if len(df_resumption) > 0:
        plt.figure(figsize=(10, 5))
        
        # Color line based on path taken
        colors = {'FAST_PFF': '#2ecc71', 'FALLBACK_BFT': '#e74c3c'}
        
        ax = sns.lineplot(x='RoundID', y='LatencyMS', data=df_resumption, color='gray', alpha=0.5, zorder=1)
        sns.scatterplot(x='RoundID', y='LatencyMS', hue='PathTaken', palette=colors, 
                        data=df_resumption, s=40, zorder=2, edgecolor='black', linewidth=0.5)
        
        # Reference lines from the originally published run. These are fixed
        # annotations, not values derived from the CSV being plotted.
        plt.axhline(y=177, color='green', linestyle='--', alpha=0.5, label='PFF reference (~177ms)')
        plt.axhline(y=331, color='red', linestyle='--', alpha=0.5, label='BFT reference (~331ms)')
        
        plt.title('Protocol Resumption & Heal Speed (Suite C.3)', fontsize=14, weight='bold')
        plt.ylabel('Latency (ms)', fontsize=12)
        plt.xlabel('Consensus Round ID', fontsize=12)
        plt.legend(title='Path Taken', loc='upper right')
        
        # Annotations for narrative
        max_y = df_resumption['LatencyMS'].max()
        plt.ylim(0, max_y * 1.2)
        
        plt.tight_layout()
        plt.savefig('figures/Graph_2_Protocol_Resumption.png', dpi=300)
        plt.close()
        print(" -> Saved figures/Graph_2_Protocol_Resumption.png")
    else:
        print(" -> Skipped Graph 2 (no data for C3_Partition)")

    print("\nAnalysis complete!")

if __name__ == "__main__":
    analyze_and_plot()
