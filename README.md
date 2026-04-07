{% extends "base.html" %}

{% block content %}
<div class="card">
    <div class="card-header">
        <h3 class="card-title">Create New Rule</h3>
    </div>
	
	<form action="{{ url_for('main.add_rule') }}" method="POST" id="ruleWizardForm" style="margin-bottom: 30px; padding-bottom: 25px; border-bottom: 1px solid var(--border-color);">
		<input type="hidden" name="csrf_token" value="{{ csrf_token() }}"/>

		<div class="wizard-progress">
			<div class="wizard-progress-bar" id="wizardBar" style="width: 33%;"></div>
		</div>

		<div class="wizard-step active" id="step-1">
			<h4 style="margin-top: 0; margin-bottom: 15px; color: var(--accent);">Step 1: What are we targeting?</h4>
			<div class="form-row" style="align-items: flex-end;">
				<div style="display: flex; flex-direction: column; gap: 4px; flex: 1;">
					<label style="font-size: 12px; color: var(--text-muted); font-weight: 500;">Target Tag</label>
					<input type="text" list="tagList" name="group_id" placeholder="e.g., ipt" required autocomplete="off">
					<datalist id="tagList">{% for tag in unique_tags %}<option value="{{ tag }}">{% endfor %}</datalist>
				</div>
				<div style="display: flex; flex-direction: column; gap: 4px; flex: 1;">
					<label style="font-size: 12px; color: var(--text-muted); font-weight: 500;">Deluge Label</label>
					<input type="text" list="labelList" name="label" placeholder="e.g., seed" required autocomplete="off">
					<datalist id="labelList">{% for label in unique_labels %}<option value="{{ label }}">{% endfor %}</datalist>
				</div>
				<div style="display: flex; flex-direction: column; gap: 4px; flex: 1;">
					<label style="font-size: 12px; color: var(--text-muted); font-weight: 500;">Torrent State</label>
					<select name="target_state">
						<option value="All">State: All</option>
						<option value="Seeding">Seeding</option>
						<option value="Paused">Paused</option>
					</select>
				</div>
			</div>
			<div style="margin-top: 20px; text-align: right;">
				<button type="button" class="btn btn-primary btn-next" data-next="2">Next Step &rarr;</button>
			</div>
		</div>

		<div class="wizard-step" id="step-2">
			<h4 style="margin-top: 0; margin-bottom: 15px; color: var(--accent);">Step 2: When should it be removed?</h4>
			<div class="form-row" style="align-items: flex-end;">
				<div style="display: flex; flex-direction: column; gap: 4px; flex: 1;">
					<label style="font-size: 12px; color: var(--text-muted); font-weight: 500;">Time Metric</label>
					<select name="time_metric">
						<option value="seeding_time">Seeding Time ></option>
						<option value="time_added">Time Since Added ></option>
						<option value="time_paused">Time Paused ></option>
					</select>
				</div>
				<div style="display: flex; flex-direction: column; gap: 4px; flex: 1.5;">
					<label style="font-size: 12px; color: var(--text-muted); font-weight: 500;">Time Threshold</label>
					<div style="display: flex; gap: 6px;">
						<input type="number" name="threshold_value" placeholder="Time" step="any" required style="width: 50%;">
						<select name="threshold_unit" style="width: 50%;">
							<option value="minutes">Minutes</option>
							<option value="hours" selected>Hours</option>
							<option value="days">Days</option>
						</select>
					</div>
				</div>
				<div style="display: flex; flex-direction: column; gap: 4px; width: 80px;">
					<label style="font-size: 12px; color: var(--text-muted); font-weight: 500;">Condition</label>
					<select name="logic_operator"><option value="OR">OR</option><option value="AND">AND</option></select>
				</div>
				<div style="display: flex; flex-direction: column; gap: 4px; flex: 1;">
					<label style="font-size: 12px; color: var(--text-muted); font-weight: 500;">Ratio Target</label>
					<input type="number" name="seed_ratio" placeholder="Ratio >" step="0.01" min="0">
				</div>
			</div>
			<div style="margin-top: 20px; display: flex; justify-content: space-between;">
				<button type="button" class="btn btn-prev" data-prev="1">&larr; Back</button>
				<button type="button" class="btn btn-primary btn-next" data-next="3">Next Step &rarr;</button>
			</div>
		</div>

		<div class="wizard-step" id="step-3">
			<h4 style="margin-top: 0; margin-bottom: 15px; color: var(--accent);">Step 3: Protections & Priorities</h4>
			<div class="form-row" style="align-items: flex-end;">
				<div style="display: flex; flex-direction: column; gap: 4px; flex: 1;">
					<label style="font-size: 12px; color: var(--text-muted); font-weight: 500;">Min Keep</label>
					<input type="number" name="min_torrents" placeholder="Min Keep" value="0" required>
				</div>
				<div style="display: flex; flex-direction: column; gap: 4px; flex: 1.5;">
					<label style="font-size: 12px; color: var(--text-muted); font-weight: 500;">Removal Priority</label>
					<select name="sort_order">
						<option value="oldest_added">Remove Oldest Added</option>
						<option value="newest_added">Remove Newest Added</option>
						<option value="longest_seeding">Remove Longest Seeding</option>
						<option value="shortest_seeding">Remove Shortest Seeding</option>
					</select>
				</div>
				<div style="display: flex; align-items: center; gap: 12px; padding-left: 15px; flex: 1;">
					<label for="delete_data_toggle" style="font-size: 13px; color: var(--text-muted); font-weight: 600;">Delete Data</label>
					<label class="toggle-switch">
						<input type="checkbox" id="delete_data_toggle" name="delete_data" value="yes">
						<span class="toggle-slider"></span>
					</label>
				</div>
			</div>
			<div style="margin-top: 20px; display: flex; justify-content: space-between;">
				<button type="button" class="btn btn-prev" data-prev="2">&larr; Back</button>
				<button type="submit" class="btn btn-success" style="background-color: var(--success); color: white;">+ Save Rule</button>
			</div>
		</div>
	</form>	
	
    <div style="display: flex; align-items: center; gap: 15px; margin-bottom: 15px; flex-wrap: wrap;">
        <h3 class="card-title" style="margin: 0;">Active Rules</h3>
        <span id="rulesCount" style="font-size: 13px; color: var(--text-muted); font-weight: 500;"></span>
    </div>
    <input type="text" id="rulesSearch" class="table-search" aria-label="Search rules" placeholder="🔍 Search rules by tag, label, or state...">
    
    <div class="table-wrapper">
        <table id="rulesTable">
            <thead>
                <tr>
                    <th class="sortable">Tag</th><th class="sortable">Label</th><th class="sortable">State</th><th class="sortable">Metric</th><th class="sortable">Threshold</th><th class="sortable">Min Keep</th><th>Sorting Priority</th><th>Delete Data?</th><th></th>
                </tr>
            </thead>
            <tbody>
                {% for rule in rules_list %}
                <tr>
                    <td><strong style="color: var(--accent);">{{ rule.get('group_id') }}</strong></td>
                    <td>{{ rule.get('label') }}</td>
                    <td><span style="background: rgba(255,255,255,0.05); padding: 4px 8px; border-radius: 4px;">{{ rule.get('target_state', 'All') }}</span></td>
                    <td>
                        {% if rule.get('time_metric') == 'time_added' %}Time Since Added
                        {% elif rule.get('time_metric') == 'time_paused' %}Time Paused
                        {% else %}Seeding Time{% endif %}
                    </td>
                    <td>
                        > {{ "%g"|format(rule.get('threshold_value', rule.get('max_hours', 0)) | float) }} {{ rule.get('threshold_unit', 'hours') }}
                        
                        {% if rule.get('seed_ratio') is not none %}
                            <br>
                            <span style="font-size: 12px; color: var(--accent); font-weight: 600;">
                                {{ rule.get('logic_operator', 'OR') }}
                            </span> 
                            <span style="font-size: 13px; color: var(--text-muted);">
                                Ratio > {{ rule.get('seed_ratio') }}
                            </span>
                        {% endif %}
                    </td>
                    <td>{{ rule.get('min_torrents', rule.get('min_keep', 0)) }}</td>
                    <td style="color: var(--text-muted);">
                        {% if rule.get('sort_order') == 'newest_added' or rule.get('sort_order') == 'newest_first' %}Newest Added
                        {% elif rule.get('sort_order') == 'longest_seeding' %}Longest Seeding
                        {% elif rule.get('sort_order') == 'shortest_seeding' %}Shortest Seeding
                        {% else %}Oldest Added{% endif %}
                    </td>
                    <td>{% if rule.get('delete_data') %}<span class="status-badge-yes">YES</span>{% else %}<span class="status-badge-no">NO</span>{% endif %}</td>
                    <td style="text-align: right;">
                        <form action="{{ url_for('main.delete_rule', index=loop.index0) }}" method="POST" style="margin:0;"
                              data-confirm-word="DELETE" data-confirm-msg="Delete this rule? Type DELETE to confirm:">
                            <input type="hidden" name="csrf_token" value="{{ csrf_token() }}"/>
                            <button type="submit" class="btn btn-danger" style="padding: 6px 10px; font-size: 12px;">Delete</button>
                        </form>
                    </td>
                </tr>
                {% else %}
                <tr data-placeholder="true"><td colspan="9" style="text-align: center; color: var(--text-muted); padding: 30px;">No automation rules configured yet.</td></tr>
                {% endfor %}
            </tbody>
        </table>
    </div>
</div>
{% endblock %}
