import React from "react";

import { mount } from "enzyme";

import fetchMock from "fetch-mock";

import toDiffableHtml from "diffable-html";

import { AlertStore } from "Stores/AlertStore";
import { Settings } from "Stores/Settings";
import { ThemeContext } from "Components/Theme";
import {
  ReactSelectColors,
  ReactSelectStyles,
} from "Components/Theme/ReactSelect";
import { MainModalContent } from "./MainModalContent";

let alertStore;
let settingsStore;
const onHide = jest.fn();

beforeEach(() => {
  alertStore = new AlertStore([]);
  settingsStore = new Settings();
  onHide.mockClear();
  fetchMock.reset();
  fetchMock.any({
    body: JSON.stringify([]),
  });
});

afterEach(() => {
  jest.restoreAllMocks();
  fetchMock.reset();
});

const Wrapped = (component) => (
  <ThemeContext.Provider
    value={{
      reactSelectStyles: ReactSelectStyles(ReactSelectColors.Light),
    }}
  >
    {component}
  </ThemeContext.Provider>
);

const FakeModal = () => {
  return mount(
    Wrapped(
      <MainModalContent
        alertStore={alertStore}
        settingsStore={settingsStore}
        onHide={onHide}
        expandAllOptions={true}
      />
    )
  );
};

const ValidateSetTab = (title) => {
  const component = FakeModal();

  const tab = component.find({ title: title });
  tab.simulate("click");
  expect(component.find(".nav-link.active").text()).toBe(title);
};

describe("<MainModalContent />", () => {
  it("matches snapshot", () => {
    // we have multiple fragments and enzyme only renders the first one
    // in html() and text(), debug() would work but it's noisy
    // https://github.com/airbnb/enzyme/issues/1213
    const tree = mount(
      <span>
        {Wrapped(
          <MainModalContent
            alertStore={alertStore}
            settingsStore={settingsStore}
            onHide={onHide}
            expandAllOptions={true}
          />
        )}
      </span>
    );
    expect(toDiffableHtml(tree.html())).toMatchSnapshot();
  });

  it("shows 'Configuration' tab by default", () => {
    const tree = FakeModal();
    const activeTab = tree.find(".nav-link.active");
    expect(activeTab.text()).toBe("Configuration");
  });

  // modal makes it tricky to verify re-rendered content, so only check if we
  // update the store for now
  it("calls setTab('configuration') after clicking on the 'Configuration' tab", () => {
    ValidateSetTab("Configuration");
  });

  it("calls setTab('help') after clicking on the 'Help' tab", () => {
    ValidateSetTab("Help");
  });

  it("shows username when alertStore.info.authentication.enabled=true", () => {
    alertStore.info.authentication.enabled = true;
    alertStore.info.authentication.username = "me@example.com";
    const tree = mount(
      <span>
        {Wrapped(
          <MainModalContent
            alertStore={alertStore}
            settingsStore={settingsStore}
            onHide={onHide}
            expandAllOptions={true}
          />
        )}
      </span>
    );
    expect(tree.text()).toMatch(/Username: me@example.com/);
  });
});
