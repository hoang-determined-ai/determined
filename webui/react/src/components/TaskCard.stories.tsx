import React from 'react';

import Grid from 'components/Grid';
import { ExperimentsDecorator } from 'storybook/ConetextDecorators';
import RouterDecorator from 'storybook/RouterDecorator';
import { ShirtSize } from 'themes';
import { RecentTask, RunState, TaskType } from 'types';
import { generateTasks } from 'utils/task';

import TaskCard from './TaskCard';

export default {
  component: TaskCard,
  decorators: [ RouterDecorator, ExperimentsDecorator ],
  title: 'TaskCard',
};

const baseProps: RecentTask = {
  id: '1a2f',
  lastEvent: {
    date: (Date.now()).toString(),
    name: 'opened',
  },
  ownerId: 5,
  progress: 0.34,
  startTime: (Date.now()).toString(),
  state: RunState.Active,
  title: 'I\'m a task',
  type: TaskType.Experiment,
  url: '#',
};

export const Default = (): React.ReactNode => {
  return <TaskCard {...baseProps} />;
};

export const InAGrid = (): React.ReactNode => {
  const tasks: React.ReactNodeArray =
    generateTasks().map((props, idx) => {
      return <TaskCard key={idx} {...props} />;
    });
  return (
    <Grid gap={ShirtSize.large} minItemWidth={20}>{tasks}</Grid>
  );
};

export const DifferentType = (): React.ReactNode => {
  const newProps: RecentTask = { ...baseProps, title: 'I\'m a shell task', type: TaskType.Shell };
  return <TaskCard {...newProps} />;
};
