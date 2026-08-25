/*
 ============================================================================
 Name        : MainActivity.java
 Author      : hev <r@hev.cc>
 Copyright   : Copyright (c) 2023 xyz
 Description : Main Activity
 ============================================================================
 */

package com.ech.workers;

import android.os.Bundle;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.DialogInterface;
import android.content.Intent;
import android.content.Context;
import android.view.View;
import android.widget.AdapterView;
import android.widget.ArrayAdapter;
import android.widget.Button;
import android.widget.CheckBox;
import android.widget.EditText;
import android.widget.Spinner;
import android.widget.Switch;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;
import android.net.VpnService;
import android.os.Handler;
import android.os.Looper;
import java.util.ArrayList;
import java.util.List;
import java.util.Set;
import java.util.UUID;

public class MainActivity extends Activity implements View.OnClickListener {
	private Preferences prefs;
    private Spinner spinner_profiles;
    private Button btn_add_profile;
    private Button btn_save_profile;
    private Button btn_rename_profile;
    private Button btn_delete_profile;
    private EditText edittext_socks_port;
    private EditText edittext_wss_addr;
    private EditText edittext_ech_dns;
    private EditText edittext_ech_domain;
    private EditText edittext_pref_ip;
    private EditText edittext_token;
    private CheckBox checkbox_global;
    private Spinner spinner_routing;
    private Switch switch_auto_best;
    private Switch switch_fake_ip;
    // IPv4/IPv6 默认启用，不在 UI 展示
    private Button button_apps;
    private Button button_control;
    private LinearLayout page_nodes;
    private View page_edit;
    private LinearLayout profile_list;
    private TextView node_summary;
    private Button tab_nodes;
    private Button tab_edit;
    private Button btn_add_node;
    private Button btn_back_nodes;
    private Button btn_save_profile_bottom;
    private boolean updatingUi;

	@Override
	public void onCreate(Bundle savedInstanceState) {
		super.onCreate(savedInstanceState);

		prefs = new Preferences(this);
		setContentView(R.layout.main);

        spinner_profiles = (Spinner) findViewById(R.id.profile_spinner);
        btn_add_profile = (Button) findViewById(R.id.btn_add_profile);
        btn_save_profile = (Button) findViewById(R.id.btn_save_profile);
        btn_rename_profile = (Button) findViewById(R.id.btn_rename_profile);
        btn_delete_profile = (Button) findViewById(R.id.btn_delete_profile);

        edittext_socks_port = (EditText) findViewById(R.id.socks_port);
        edittext_wss_addr = (EditText) findViewById(R.id.wss_addr);
        edittext_ech_dns = (EditText) findViewById(R.id.ech_dns);
        edittext_ech_domain = (EditText) findViewById(R.id.ech_domain);
        edittext_pref_ip = (EditText) findViewById(R.id.pref_ip);
        edittext_token = (EditText) findViewById(R.id.token);
        checkbox_global = (CheckBox) findViewById(R.id.global);
        spinner_routing = (Spinner) findViewById(R.id.routing_mode);
        switch_auto_best = (Switch) findViewById(R.id.auto_best);
        switch_fake_ip = (Switch) findViewById(R.id.fake_ip);
        button_apps = (Button) findViewById(R.id.apps);
        button_control = (Button) findViewById(R.id.control);
        page_nodes = (LinearLayout) findViewById(R.id.page_nodes);
        page_edit = (LinearLayout) findViewById(R.id.page_edit);
        profile_list = (LinearLayout) findViewById(R.id.profile_list);
        node_summary = (TextView) findViewById(R.id.node_summary);
        tab_nodes = (Button) findViewById(R.id.tab_nodes);
        tab_edit = (Button) findViewById(R.id.tab_edit);
        btn_add_node = (Button) findViewById(R.id.btn_add_node);
        btn_back_nodes = (Button) findViewById(R.id.btn_back_nodes);
        btn_save_profile_bottom = (Button) findViewById(R.id.btn_save_profile_bottom);

        btn_add_profile.setOnClickListener(this);
        btn_save_profile.setOnClickListener(this);
        btn_rename_profile.setOnClickListener(this);
        btn_delete_profile.setOnClickListener(this);
        checkbox_global.setOnClickListener(this);
        switch_auto_best.setOnClickListener(this);
        switch_fake_ip.setOnClickListener(this);
        button_apps.setOnClickListener(this);
        button_control.setOnClickListener(this);
        tab_nodes.setOnClickListener(this);
        tab_edit.setOnClickListener(this);
        btn_add_node.setOnClickListener(this);
        btn_back_nodes.setOnClickListener(this);
        btn_save_profile_bottom.setOnClickListener(this);
        
        initProfileSpinner();
        updateUI();
        showPage(false);

		/* Request VPN permission */

        Intent intent = VpnService.prepare(MainActivity.this);
		if (intent != null)
		  startActivityForResult(intent, 0);
		else
		  onActivityResult(0, RESULT_OK, null);
	}

    private class ProfileItem {
        String id;
        String name;
        
        ProfileItem(String id, String name) {
            this.id = id;
            this.name = name;
        }
        
        @Override
        public String toString() {
            return name;
        }
        
        @Override
        public boolean equals(Object o) {
            if (this == o) return true;
            if (o == null || getClass() != o.getClass()) return false;
            ProfileItem that = (ProfileItem) o;
            return id.equals(that.id);
        }
    }

    private void initProfileSpinner() {
        refreshProfileSpinner();
        
        spinner_profiles.setOnItemSelectedListener(new AdapterView.OnItemSelectedListener() {
            @Override
            public void onItemSelected(AdapterView<?> parent, View view, int position, long id) {
                ProfileItem item = (ProfileItem) parent.getItemAtPosition(position);
                if (!item.id.equals(prefs.getCurrentProfileId())) {
                    savePrefs(); // Save current profile before switching
                    prefs.setCurrentProfileId(item.id);
                    updateUI();
                }
            }

            @Override
            public void onNothingSelected(AdapterView<?> parent) {
            }
        });
    }

    private void refreshProfileSpinner() {
        Set<String> ids = prefs.getProfileIds();
        List<ProfileItem> items = new ArrayList<>();
        String currentId = prefs.getCurrentProfileId();

        for (String id : ids) {
            String name = prefs.getProfileName(id);
            items.add(new ProfileItem(id, name));
        }

        java.util.Collections.sort(items, new java.util.Comparator<ProfileItem>() {
            @Override
            public int compare(ProfileItem a, ProfileItem b) {
                return a.name.compareToIgnoreCase(b.name);
            }
        });

        int selectedIndex = 0;
        for (int i = 0; i < items.size(); i++) {
            if (items.get(i).id.equals(currentId)) {
                selectedIndex = i;
                break;
            }
        }
        
        ArrayAdapter<ProfileItem> adapter = new ArrayAdapter<>(this, android.R.layout.simple_spinner_item, items);
        adapter.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item);
        spinner_profiles.setAdapter(adapter);
        spinner_profiles.setSelection(selectedIndex);
    }

    private int dp(int value) {
        return (int) (value * getResources().getDisplayMetrics().density + 0.5f);
    }

    private void showPage(boolean edit) {
        page_nodes.setVisibility(edit ? View.GONE : View.VISIBLE);
        page_edit.setVisibility(edit ? View.VISIBLE : View.GONE);
        tab_nodes.setSelected(!edit);
        tab_edit.setSelected(edit);
        tab_nodes.setBackgroundTintList(android.content.res.ColorStateList.valueOf(edit ? 0xFFE2E8F0 : 0xFF2563EB));
        tab_edit.setBackgroundTintList(android.content.res.ColorStateList.valueOf(edit ? 0xFF2563EB : 0xFFE2E8F0));
        tab_nodes.setTextColor(edit ? 0xFF475569 : 0xFFFFFFFF);
        tab_edit.setTextColor(edit ? 0xFFFFFFFF : 0xFF475569);
    }

    private List<ProfileItem> getSortedProfiles() {
        Set<String> ids = prefs.getProfileIds();
        List<ProfileItem> items = new ArrayList<>();
        for (String id : ids) {
            items.add(new ProfileItem(id, prefs.getProfileName(id)));
        }
        java.util.Collections.sort(items, new java.util.Comparator<ProfileItem>() {
            @Override
            public int compare(ProfileItem a, ProfileItem b) {
                return a.name.compareToIgnoreCase(b.name);
            }
        });
        return items;
    }

    private void refreshProfileList() {
        profile_list.removeAllViews();
        final String currentId = prefs.getCurrentProfileId();
        boolean editable = !prefs.getEnable();
        for (final ProfileItem item : getSortedProfiles()) {
            LinearLayout row = new LinearLayout(this);
            row.setOrientation(LinearLayout.HORIZONTAL);
            row.setGravity(android.view.Gravity.CENTER_VERTICAL);
            row.setPadding(dp(14), dp(10), dp(8), dp(10));
            row.setBackgroundResource(R.drawable.panel_background);
            LinearLayout.LayoutParams rowParams = new LinearLayout.LayoutParams(
                    LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT);
            rowParams.setMargins(0, 0, 0, dp(10));
            profile_list.addView(row, rowParams);

            LinearLayout text = new LinearLayout(this);
            text.setOrientation(LinearLayout.VERTICAL);
            text.setLayoutParams(new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1));
            TextView name = new TextView(this);
            name.setText(item.name + (item.id.equals(currentId) ? "  ·  当前" : ""));
            name.setTextColor(0xFF172033);
            name.setTextSize(16);
            name.setTypeface(null, android.graphics.Typeface.BOLD);
            TextView address = new TextView(this);
            String addr = prefs.getWssAddrFor(item.id);
            address.setText(addr.isEmpty() ? getString(R.string.node_not_configured) : addr);
            address.setTextColor(0xFF64748B);
            address.setTextSize(12);
            address.setMaxLines(1);
            address.setEllipsize(android.text.TextUtils.TruncateAt.END);
            text.addView(name);
            text.addView(address);
            row.addView(text);

            Button select = new Button(this);
            select.setText(item.id.equals(currentId) ? R.string.node_selected : R.string.node_select);
            select.setTextSize(11);
            select.setEnabled(editable && !item.id.equals(currentId));
            row.addView(select, new LinearLayout.LayoutParams(dp(66), dp(42)));
            Button edit = new Button(this);
            edit.setText(R.string.btn_edit);
            edit.setTextSize(11);
            edit.setEnabled(editable);
            row.addView(edit, new LinearLayout.LayoutParams(dp(66), dp(42)));

            View.OnClickListener selectListener = new View.OnClickListener() {
                @Override
                public void onClick(View view) {
                    if (!editable) {
                        return;
                    }
                    savePrefs();
                    prefs.setCurrentProfileId(item.id);
                    updateUI();
                }
            };
            row.setOnClickListener(selectListener);
            select.setOnClickListener(selectListener);
            edit.setOnClickListener(new View.OnClickListener() {
                @Override
                public void onClick(View view) {
                    if (!editable) {
                        return;
                    }
                    savePrefs();
                    prefs.setCurrentProfileId(item.id);
                    updateUI();
                    showPage(true);
                }
            });
        }
    }

    @Override
    protected void onActivityResult(int request, int result, Intent data) {
        if ((result == RESULT_OK) && prefs.getEnable()) {
            // 仅当用户手动启用时才启动服务
            Intent intent = new Intent(this, TProxyService.class);
            startService(intent.setAction(TProxyService.ACTION_CONNECT));
        }
    }

    private void showAddProfileDialog() {
        final EditText input = new EditText(this);
        input.setHint(R.string.dialog_hint_name);
        final AlertDialog dialog = new AlertDialog.Builder(this)
            .setTitle(R.string.dialog_title_add)
            .setView(input)
            .setPositiveButton(R.string.ok, null) // Set null first, override later
            .setNegativeButton(R.string.cancel, null)
            .create();

        dialog.setOnShowListener(new DialogInterface.OnShowListener() {
            @Override
            public void onShow(DialogInterface d) {
                Button button = dialog.getButton(AlertDialog.BUTTON_POSITIVE);
                button.setOnClickListener(new View.OnClickListener() {
                    @Override
                    public void onClick(View view) {
                        String name = input.getText().toString().trim();
                        if (name.isEmpty()) {
                            Toast.makeText(MainActivity.this, R.string.toast_name_empty, Toast.LENGTH_SHORT).show();
                            return;
                        }
                        // Check dup name
                        for (String id : prefs.getProfileIds()) {
                            if (prefs.getProfileName(id).equals(name)) {
                                Toast.makeText(MainActivity.this, R.string.toast_profile_exists, Toast.LENGTH_SHORT).show();
                                return;
                            }
                        }
                        String newId = UUID.randomUUID().toString();
                        savePrefs(); // Save current before switching
                        prefs.addProfile(newId, name);
                        prefs.setCurrentProfileId(newId);
                        refreshProfileSpinner();
                        updateUI();
                        showPage(true);
                        dialog.dismiss();
                    }
                });
            }
        });
        dialog.show();
    }

    private void showRenameProfileDialog() {
        final String currentId = prefs.getCurrentProfileId();
        final EditText input = new EditText(this);
        input.setText(prefs.getProfileName(currentId));
        new AlertDialog.Builder(this)
            .setTitle(R.string.dialog_title_rename)
            .setView(input)
            .setPositiveButton(R.string.ok, new DialogInterface.OnClickListener() {
                public void onClick(DialogInterface dialog, int whichButton) {
                    String name = input.getText().toString().trim();
                    if (!name.isEmpty()) {
                         // Check dup name
                        for (String id : prefs.getProfileIds()) {
                            if (!id.equals(currentId) && prefs.getProfileName(id).equals(name)) {
                                Toast.makeText(MainActivity.this, R.string.toast_profile_exists, Toast.LENGTH_SHORT).show();
                                return;
                            }
                        }
                        prefs.setProfileName(currentId, name);
                        refreshProfileSpinner();
                    }
                }
            })
            .setNegativeButton(R.string.cancel, null)
            .show();
    }

    private void deleteCurrentProfile() {
        final String currentId = prefs.getCurrentProfileId();
        Set<String> ids = prefs.getProfileIds();
        if (ids.size() <= 1) {
            Toast.makeText(this, R.string.toast_cannot_delete_last, Toast.LENGTH_SHORT).show();
            return;
        }

        new AlertDialog.Builder(this)
            .setTitle(R.string.dialog_title_delete)
            .setPositiveButton(R.string.ok, new DialogInterface.OnClickListener() {
                @Override
                public void onClick(DialogInterface dialog, int which) {
                    prefs.removeProfile(currentId);
                    // Switch to another one
                    String nextId = prefs.getProfileIds().iterator().next();
                    prefs.setCurrentProfileId(nextId);
                    refreshProfileSpinner();
                    updateUI();
                }
            })
            .setNegativeButton(R.string.cancel, null)
            .show();
    }

	@Override
	public void onClick(View view) {
        if (view == tab_nodes) {
            if (!prefs.getEnable()) {
                savePrefs();
            }
            showPage(false);
        } else if (view == tab_edit) {
            showPage(true);
        } else if (view == btn_add_node) {
            showAddProfileDialog();
        } else if (view == btn_back_nodes) {
            if (!prefs.getEnable()) {
                savePrefs();
            }
            showPage(false);
            updateUI();
        } else if (view == checkbox_global || view == switch_auto_best || view == switch_fake_ip) {
            savePrefs();
            updateUI();
        } else if (view == button_apps) {
            startActivity(new Intent(this, AppListActivity.class));
        } else if (view == btn_add_profile) {
            showAddProfileDialog();
        } else if (view == btn_save_profile || view == btn_save_profile_bottom) {
            String wssAddr = edittext_wss_addr.getText().toString().trim();
            if (wssAddr.isEmpty()) {
                Toast.makeText(this, "服务器地址不能为空", Toast.LENGTH_SHORT).show();
                return;
            }
            savePrefs();
            Toast.makeText(this, R.string.toast_saved, Toast.LENGTH_SHORT).show();
            if (view == btn_save_profile_bottom) {
                updateUI();
                showPage(false);
            }
        } else if (view == btn_rename_profile) {
            showRenameProfileDialog();
        } else if (view == btn_delete_profile) {
            deleteCurrentProfile();
        } else if (view == button_control) {
            boolean isEnable = prefs.getEnable();
            if (isEnable) {
                prefs.setEnable(false);
                updateUI();
                // 停用：先发 DISCONNECT，再延迟 200ms 发送一个 stopSelf
                startService(new Intent(this, TProxyService.class).setAction(TProxyService.ACTION_DISCONNECT));
            } else {
                String wssAddr = edittext_wss_addr.getText().toString().trim();
                if (wssAddr.isEmpty()) {
                    Toast.makeText(this, "服务器地址不能为空", Toast.LENGTH_SHORT).show();
                    return;
                }
                savePrefs();
                prefs.setEnable(true);
                updateUI();
                startService(new Intent(this, TProxyService.class).setAction(TProxyService.ACTION_CONNECT));
            }
        }
	}

	private void updateUI() {
        edittext_socks_port.setText(Integer.toString(prefs.getSocksPort()));
        edittext_wss_addr.setText(prefs.getWssAddr());
        edittext_ech_dns.setText(prefs.getEchDns());
        edittext_ech_domain.setText(prefs.getEchDomain());
        edittext_pref_ip.setText(prefs.getPrefIp());
        edittext_token.setText(prefs.getToken());
        checkbox_global.setChecked(prefs.getGlobal());
        String routing = prefs.getRoutingMode();
        spinner_routing.setSelection("global".equals(routing) ? 1 : ("none".equals(routing) ? 2 : 0));
        switch_auto_best.setChecked(prefs.getAutoBest());
        switch_fake_ip.setChecked(prefs.getFakeIp());
        node_summary.setText(prefs.getProfileName(prefs.getCurrentProfileId()) + "  ·  "
                + (prefs.getEnable() ? getString(R.string.node_running) : getString(R.string.node_stopped)));
        refreshProfileList();

        boolean editable = !prefs.getEnable();
        edittext_socks_port.setEnabled(editable);
        edittext_wss_addr.setEnabled(editable);
        edittext_ech_dns.setEnabled(editable);
        edittext_ech_domain.setEnabled(editable);
        edittext_pref_ip.setEnabled(editable);
        edittext_token.setEnabled(editable);
        checkbox_global.setEnabled(editable);
        spinner_routing.setEnabled(editable);
        switch_auto_best.setEnabled(editable);
        switch_fake_ip.setEnabled(editable);
        
        boolean globalChecked = checkbox_global.isChecked();
        button_apps.setEnabled(editable && !globalChecked);
        if (button_apps.isEnabled()) {
             button_apps.setBackgroundTintList(android.content.res.ColorStateList.valueOf(0xFF9C27B0)); // Purple
        } else {
             button_apps.setBackgroundTintList(android.content.res.ColorStateList.valueOf(0xFFBDBDBD)); // Grey
        }
        
        spinner_profiles.setEnabled(editable);
        btn_add_profile.setEnabled(editable);
        btn_save_profile.setEnabled(editable);
        btn_rename_profile.setEnabled(editable);
        btn_delete_profile.setEnabled(editable);

        int grey = 0xFFBDBDBD;
        spinner_profiles.setAlpha(editable ? 1.0f : 0.5f);
        spinner_routing.setAlpha(editable ? 1.0f : 0.5f);
        btn_add_profile.setBackgroundTintList(android.content.res.ColorStateList.valueOf(editable ? 0xFF4CAF50 : grey));
        btn_save_profile.setBackgroundTintList(android.content.res.ColorStateList.valueOf(editable ? 0xFF2196F3 : grey));
        btn_rename_profile.setBackgroundTintList(android.content.res.ColorStateList.valueOf(editable ? 0xFFFF9800 : grey));
        btn_delete_profile.setBackgroundTintList(android.content.res.ColorStateList.valueOf(editable ? 0xFFF44336 : grey));

        if (editable) {
          button_control.setText(R.string.control_enable);
          button_control.setBackgroundTintList(android.content.res.ColorStateList.valueOf(0xFF4CAF50)); // Green
        } else {
          button_control.setText(R.string.control_disable);
          button_control.setBackgroundTintList(android.content.res.ColorStateList.valueOf(0xFFF44336)); // Red
        }
	}

	private void savePrefs() {
        int port = 1080;
        try {
            port = Integer.parseInt(edittext_socks_port.getText().toString());
        } catch (Exception e) {
        }
        if (port < 1024) {
            port = 1024;
            edittext_socks_port.setText(Integer.toString(port));
            Toast.makeText(getApplicationContext(), "端口已设置为≥1024", Toast.LENGTH_SHORT).show();
        }
        prefs.setSocksPort(port);
        prefs.setWssAddr(edittext_wss_addr.getText().toString());
        prefs.setEchDns(edittext_ech_dns.getText().toString());
        prefs.setEchDomain(edittext_ech_domain.getText().toString());
        prefs.setPrefIp(edittext_pref_ip.getText().toString());
        prefs.setToken(edittext_token.getText().toString());
        String routing = "bypass_cn";
        if (spinner_routing.getSelectedItemPosition() == 1) {
            routing = "global";
        } else if (spinner_routing.getSelectedItemPosition() == 2) {
            routing = "none";
        }
        prefs.setRoutingMode(routing);
        prefs.setAutoBest(switch_auto_best.isChecked());
        prefs.setFakeIp(switch_fake_ip.isChecked());
        
        // IPv4/IPv6 默认启用
        prefs.setIpv4(true);
        prefs.setIpv6(true);
        prefs.setGlobal(checkbox_global.isChecked());
        prefs.setUdpInTcp(false);
        prefs.setRemoteDns(true);
    }
}
